// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package load

import (
	"context"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/api"
	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestRunMeasuresASmallProfile(t *testing.T) {
	box := &mailbox{boxes: map[string][]mailbackend.Message{}, state: map[string]int{}}
	svc := service.New(box)
	svc.SetSyncLimit(1)
	svc.SetQuota(1000, 1000)
	feed := events.New(&store.Memory{})
	svc.SetFeed(feed)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(15 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = feed.Sweep(ctx, box)
			}
		}
	}()
	srv := httptest.NewServer((&api.Server{
		Service: svc, Token: "operator-token", Domain: "example.com", DomainID: "dom", RoleID: "role",
	}).Handler())
	defer srv.Close()

	report, err := Run(ctx, Options{
		BaseURL: srv.URL, Token: "operator-token", Prefix: "load",
		Inboxes: 4, Messages: 1, Concurrency: 2, EventWait: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Errors != 0 || report.Inboxes != 4 || report.Sends != 4 || report.Lists != 4 || report.Reads != 1 {
		t.Fatalf("report %+v", report)
	}
	if report.Events != "seen" || report.EventLagMS < 0 {
		t.Fatalf("event %s lag %v", report.Events, report.EventLagMS)
	}
	if report.SendP95MS < report.SendP50MS || report.ListP95MS < report.ListP50MS {
		t.Fatalf("percentiles %+v", report)
	}
}

type mailbox struct {
	mu    sync.Mutex
	n     int
	boxes map[string][]mailbackend.Message
	state map[string]int
}

func (m *mailbox) CreateInbox(context.Context, mailbackend.InboxSpec) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	id := "id-" + strconv.Itoa(m.n)
	m.boxes[id] = nil
	m.state[id] = 0
	return id, nil
}

func (m *mailbox) CreateInboxes(_ context.Context, specs []mailbackend.InboxSpec) ([]string, error) {
	ids := make([]string, len(specs))
	for i := range specs {
		id, err := m.CreateInbox(context.Background(), specs[i])
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}
	return ids, nil
}

func (m *mailbox) Deliver(_ context.Context, msg mailbackend.Outbound) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state[msg.AccountID]++
	m.boxes[msg.AccountID] = append(m.boxes[msg.AccountID], mailbackend.Message{
		ID: "m-" + strconv.Itoa(len(m.boxes[msg.AccountID])+1), Subject: msg.Subject, Text: msg.Body,
	})
	return nil
}

func (m *mailbox) List(_ context.Context, id string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return mailbackend.Slice(append([]mailbackend.Message(nil), m.boxes[id]...), func(msg mailbackend.Message) string { return msg.ID }, page)
}

func (m *mailbox) Search(ctx context.Context, id, _ string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	return m.List(ctx, id, page)
}

func (m *mailbox) Get(_ context.Context, id, messageID string) (mailbackend.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.boxes[id] {
		if msg.ID == messageID {
			return msg, nil
		}
	}
	return mailbackend.Message{}, nil
}

func (m *mailbox) Delete(context.Context, string, string) error { return nil }

func (m *mailbox) ListInboxes(context.Context, mailbackend.Page) ([]mailbackend.Account, string, error) {
	return nil, "", nil
}

func (m *mailbox) DeleteInbox(context.Context, string) error { return nil }

func (m *mailbox) Download(context.Context, string, string) ([]byte, error) { return nil, nil }

func (m *mailbox) MailState(_ context.Context, inbox string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strconv.Itoa(m.state[inbox]), nil
}

func (m *mailbox) MailChanges(_ context.Context, inbox, since string) (mailbackend.Delta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.state[inbox]
	old, _ := strconv.Atoi(since)
	delta := mailbackend.Delta{State: strconv.Itoa(current)}
	if old < current {
		for _, msg := range m.boxes[inbox] {
			delta.Created = append(delta.Created, msg.ID)
		}
	}
	return delta, nil
}
