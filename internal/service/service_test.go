// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

func TestAgentCannotGrantItself(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "secret"}}}
	svc := New(mail)
	if err := svc.Grant(Caller{Agent: "ada"}, "ada", "inbox-a", PermRead); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
	if _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a"); !errors.Is(err, ErrForbidden) || mail.listed {
		t.Fatalf("list err %v listed %v", err, mail.listed)
	}
	if err := svc.Grant(Caller{Operator: true}, "ada", "inbox-a", PermRead); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a"); err != nil || !mail.listed {
		t.Fatalf("granted list err %v listed %v", err, mail.listed)
	}
}

func TestAgentWithoutAGrantCannotRead(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "secret"}}}
	svc := New(mail)
	_, err := svc.List(context.Background(), Caller{Agent: "eve"}, "inbox-a")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
	if mail.listed {
		t.Fatal("the mail backend was called without a grant")
	}
}

func TestReadGrantAllowsOnlyThatInbox(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "hello"}}}
	svc := New(mail)
	svc.Allow("ada", "inbox-a", PermRead)
	got, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a")
	if err != nil || len(got) != 1 {
		t.Fatalf("list %+v %v", got, err)
	}
	if _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-b"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other inbox err = %v", err)
	}
	if err := svc.Deliver(context.Background(), Caller{Agent: "ada"}, mailbackend.Outbound{AccountID: "inbox-a"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("send without send grant err = %v", err)
	}
}

func TestOperatorCanCreateAnInbox(t *testing.T) {
	mail := &fakeMail{created: "id-1"}
	svc := New(mail)
	id, err := svc.CreateInbox(context.Background(), Caller{Operator: true}, mailbackend.InboxSpec{Local: "alice"}, "")
	if err != nil || id != "id-1" {
		t.Fatalf("create %s %v", id, err)
	}
	if _, err := svc.CreateInbox(context.Background(), Caller{Agent: "ada"}, mailbackend.InboxSpec{Local: "bob"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent create err = %v", err)
	}
}

func TestSendQuotaAndIdempotency(t *testing.T) {
	mail := &fakeMail{}
	svc := New(mail)
	svc.inboxDaily = 1
	svc.domainDaily = 2
	msg := mailbackend.Outbound{AccountID: "inbox-a", From: "a@example.com", To: "b@example.com", Subject: "hi"}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, msg, "key-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, msg, "key-1"); err != nil {
		t.Fatal(err)
	}
	if mail.sent != 1 {
		t.Fatalf("backend sends = %d", mail.sent)
	}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, msg, "key-2"); !errors.Is(err, ErrQuota) {
		t.Fatalf("quota err = %v", err)
	}
	if mail.sent != 1 {
		t.Fatalf("quota still called the backend, sends = %d", mail.sent)
	}
}

func TestFailedSendDoesNotConsumeQuota(t *testing.T) {
	mail := &fakeMail{sendErr: errors.New("down")}
	svc := New(mail)
	svc.inboxDaily = 1
	msg := mailbackend.Outbound{AccountID: "inbox-a", From: "a@example.com", To: "b@example.com"}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, msg, ""); err == nil {
		t.Fatal("expected send error")
	}
	mail.sendErr = nil
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, msg, ""); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateRequiresAnOperatorAndBatches(t *testing.T) {
	mail := &fakeMail{}
	svc := New(mail)
	_, err := svc.Generate(context.Background(), Caller{Agent: "ada"}, GenerateRequest{
		Prefix: "u", Domain: "example.com", DomainID: "dom", RoleID: "role", Count: 2, Wait: true,
	}, "")
	if !errors.Is(err, ErrForbidden) || len(mail.batches) != 0 {
		t.Fatalf("err %v batches %v", err, mail.batches)
	}
	got, err := svc.Generate(context.Background(), Caller{Operator: true}, GenerateRequest{
		Prefix: "u", Domain: "example.com", DomainID: "dom", RoleID: "role", Count: 2, Wait: true,
	}, "gen-1")
	if err != nil || len(got.Inboxes) != 2 || got.Inboxes[0].Address != "u00000@example.com" {
		t.Fatalf("generate %+v %v", got, err)
	}
	again, err := svc.Generate(context.Background(), Caller{Operator: true}, GenerateRequest{
		Prefix: "u", Domain: "example.com", DomainID: "dom", RoleID: "role", Count: 2, Wait: true,
	}, "gen-1")
	if err != nil || again.Inboxes[0].ID != got.Inboxes[0].ID || len(mail.batches) != 1 {
		t.Fatalf("replay %+v batches %v err %v", again, mail.batches, err)
	}
}

func TestGenerateAboveTheLimitReturnsAJob(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mail := &fakeMail{started: started, release: release}
	svc := New(mail)
	svc.SetSyncLimit(1)
	got, err := svc.Generate(context.Background(), Caller{Operator: true}, GenerateRequest{
		Prefix: "u", Domain: "example.com", DomainID: "dom", RoleID: "role", Count: 2,
	}, "")
	if err != nil || got.JobID == "" {
		t.Fatalf("job %+v %v", got, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	job, err := svc.Job(got.JobID)
	if err != nil || job.Status != "running" {
		t.Fatalf("running %+v %v", job, err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, err = svc.Job(got.JobID)
		if err == nil && job.Status == "done" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if job.Status != "done" || job.Created != 2 {
		t.Fatalf("finished %+v", job)
	}
}

func TestReplyAndDeleteUseTheirOwnGrants(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{
		ID: "m", Subject: "hello", MessageID: []string{"<a@example.com>"},
	}}}
	svc := New(mail)
	svc.Allow("ada", "inbox-a", PermRead)
	if _, err := svc.Search(context.Background(), Caller{Agent: "ada"}, "inbox-b", "hello"); !errors.Is(err, ErrForbidden) || mail.listed {
		t.Fatalf("search err %v listed %v", err, mail.listed)
	}
	if err := svc.Delete(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m"); !errors.Is(err, ErrForbidden) || mail.deleted {
		t.Fatalf("delete err %v deleted %v", err, mail.deleted)
	}
	reply := mailbackend.Outbound{From: "a@example.com", To: "b@example.com"}
	if err := svc.Reply(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m", reply, ""); !errors.Is(err, ErrForbidden) || mail.got {
		t.Fatalf("reply err %v got %v", err, mail.got)
	}
	svc.Allow("ada", "inbox-a", PermReply)
	if err := svc.Reply(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m", reply, ""); err != nil {
		t.Fatal(err)
	}
	if mail.last.Subject != "Re: hello" || len(mail.last.InReplyTo) != 1 || mail.last.InReplyTo[0] != "<a@example.com>" {
		t.Fatalf("reply %+v", mail.last)
	}
	svc.Allow("ada", "inbox-a", PermDelete)
	if err := svc.Delete(context.Background(), Caller{Agent: "ada"}, "inbox-b", "m"); !errors.Is(err, ErrForbidden) || mail.deleted {
		t.Fatalf("other inbox err %v deleted %v", err, mail.deleted)
	}
	if err := svc.Delete(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m"); err != nil || !mail.deleted {
		t.Fatalf("delete err %v deleted %v", err, mail.deleted)
	}
}

type fakeMail struct {
	created  string
	messages []mailbackend.Message
	listed   bool
	sent     int
	sendErr  error
	got      bool
	deleted  bool
	last     mailbackend.Outbound
	batches  []int
	started  chan struct{}
	release  chan struct{}
}

func (f *fakeMail) CreateInbox(context.Context, mailbackend.InboxSpec) (string, error) {
	return f.created, nil
}

func (f *fakeMail) CreateInboxes(_ context.Context, specs []mailbackend.InboxSpec) ([]string, error) {
	if f.started != nil {
		select {
		case <-f.started:
		default:
			close(f.started)
		}
	}
	if f.release != nil {
		<-f.release
	}
	f.batches = append(f.batches, len(specs))
	ids := make([]string, len(specs))
	for i, spec := range specs {
		ids[i] = spec.Local
	}
	return ids, nil
}

func (f *fakeMail) Deliver(_ context.Context, msg mailbackend.Outbound) error {
	f.sent++
	f.last = msg
	return f.sendErr
}

func (f *fakeMail) List(context.Context, string) ([]mailbackend.Message, error) {
	f.listed = true
	return f.messages, nil
}

func (f *fakeMail) Search(context.Context, string, string) ([]mailbackend.Message, error) {
	f.listed = true
	return f.messages, nil
}

func (f *fakeMail) Get(context.Context, string, string) (mailbackend.Message, error) {
	f.got = true
	if len(f.messages) == 0 {
		return mailbackend.Message{}, nil
	}
	return f.messages[0], nil
}

func (f *fakeMail) Delete(context.Context, string, string) error {
	f.deleted = true
	return nil
}
