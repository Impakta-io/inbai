// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/jobs"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
)

type stubWorkerMail struct {
	created [][]mailbackend.InboxSpec
	swept   int
	deleted []string
}

func (s *stubWorkerMail) CreateInbox(context.Context, mailbackend.InboxSpec) (string, error) {
	return "id", nil
}
func (s *stubWorkerMail) CreateInboxes(_ context.Context, specs []mailbackend.InboxSpec) ([]string, error) {
	s.created = append(s.created, specs)
	ids := make([]string, len(specs))
	for i, spec := range specs {
		ids[i] = "id-" + spec.Local
	}
	return ids, nil
}
func (s *stubWorkerMail) Deliver(context.Context, mailbackend.Outbound) error { return nil }
func (s *stubWorkerMail) SenderAddresses(_ context.Context, account string) ([]string, error) {
	return []string{account + "@one.test"}, nil
}
func (s *stubWorkerMail) List(context.Context, string, mailbackend.Page) ([]mailbackend.Message, string, error) {
	return []mailbackend.Message{{ID: "old", ReceivedAt: time.Now().Add(-40 * 24 * time.Hour)}}, "", nil
}
func (s *stubWorkerMail) Search(context.Context, string, string, mailbackend.Page) ([]mailbackend.Message, string, error) {
	return nil, "", nil
}
func (s *stubWorkerMail) Get(context.Context, string, string) (mailbackend.Message, error) {
	return mailbackend.Message{}, nil
}
func (s *stubWorkerMail) Delete(_ context.Context, _, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}
func (s *stubWorkerMail) ListInboxes(context.Context, mailbackend.Page) ([]mailbackend.Account, string, error) {
	return []mailbackend.Account{{ID: "inbox-a", Local: "ada"}}, "", nil
}
func (s *stubWorkerMail) DeleteInbox(context.Context, string) error { return nil }
func (s *stubWorkerMail) Download(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (s *stubWorkerMail) MailState(context.Context, string) (string, error) { return "s0", nil }
func (s *stubWorkerMail) MailChanges(context.Context, string, string) (mailbackend.Delta, error) {
	s.swept++
	return mailbackend.Delta{State: "s1", Created: []string{"m1"}}, nil
}

func TestWorkerRunsBulkCursorAndRetentionJobs(t *testing.T) {
	ctx := context.Background()
	memLog := &store.Memory{}
	if err := memLog.Put(ctx, "inbox-a", "ada@example.com", "s0", nil); err != nil {
		t.Fatal(err)
	}
	mail := &stubWorkerMail{}
	svc := service.New(mail)
	feed := events.New(memLog)
	q := &store.MemoryQueue{}
	var stderr bytes.Buffer
	worker := newWorker(q, svc, feed, mail, 30, &stderr)

	bulkRaw, err := json.Marshal(service.BulkPayload{
		JobID: "j1", Prefix: "u", Domain: "example.com", DomainID: "d", RoleID: "r", Count: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Second)
	for _, item := range []struct {
		kind    string
		key     string
		payload []byte
	}{
		{jobs.KindBulkGenerate, "j1", bulkRaw},
		{jobs.KindCursorSweep, "singleton", []byte(`{}`)},
		{jobs.KindRetentionSweep, "singleton", []byte(`{}`)},
	} {
		if _, ok, err := q.Enqueue(ctx, item.kind, item.key, item.payload, now); err != nil || !ok {
			t.Fatalf("enqueue %s ok=%v err=%v", item.kind, ok, err)
		}
	}

	for range 3 {
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("run ok=%v err=%v stderr=%s", ok, err, stderr.String())
		}
	}
	if len(mail.created) != 1 || len(mail.created[0]) != 2 {
		t.Fatalf("bulk created %+v", mail.created)
	}
	if mail.swept != 1 {
		t.Fatalf("cursor swept %d", mail.swept)
	}
	if len(mail.deleted) != 1 || mail.deleted[0] != "old" {
		t.Fatalf("retention deleted %+v", mail.deleted)
	}
	for _, job := range q.Jobs() {
		if !job.Done || job.Failed {
			t.Fatalf("job not done %+v", job)
		}
	}
}
