// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestEventsWithoutAGrantDoNotReadTheLog(t *testing.T) {
	log := &countingLog{Log: &store.Memory{}}
	svc := New(&eventMail{})
	svc.SetFeed(events.New(log))
	if _, err := svc.Events(context.Background(), Caller{Agent: "ada"}, "inbox-b", 0, 0); !errors.Is(err, ErrForbidden) || log.since != 0 {
		t.Fatalf("err %v reads %d", err, log.since)
	}
	if err := svc.Grant(Caller{Operator: true}, "ada", "inbox-a", PermRead); err != nil {
		t.Fatal(err)
	}
	if err := log.Put(context.Background(), "inbox-a", "", "s1", []store.Record{{MessageID: "m", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	records, err := svc.Events(context.Background(), Caller{Agent: "ada"}, "inbox-a", 0, 0)
	if err != nil || len(records) != 1 || records[0].MessageID != "m" || log.since != 1 {
		t.Fatalf("records %+v err %v reads %d", records, err, log.since)
	}
}

func TestIngestHintAppendsAChange(t *testing.T) {
	log := &store.Memory{}
	feed := events.New(log)
	svc := New(&eventMail{state: "s0", next: mailbackend.Delta{State: "s1", Created: []string{"m"}}})
	svc.SetFeed(feed)
	if err := svc.BindInbox(context.Background(), "inbox-a", "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	synced, err := svc.Ingest(context.Background(), []string{"ada@example.com"})
	if err != nil || synced != 1 {
		t.Fatalf("synced %d err %v", synced, err)
	}
	records, err := feed.Since(context.Background(), "inbox-a", 0, 10)
	if err != nil || len(records) != 1 || records[0].MessageID != "m" {
		t.Fatalf("records %+v err %v", records, err)
	}
	restarted := events.New(log)
	again, err := restarted.Since(context.Background(), "inbox-a", 0, 10)
	if err != nil || len(again) != 1 {
		t.Fatalf("after restart %+v err %v", again, err)
	}
}

func TestWebhookRequiresAReadGrant(t *testing.T) {
	svc := New(&eventMail{})
	svc.SetFeed(events.New(&store.Memory{}))
	if _, _, err := svc.SubscribeWebhook(context.Background(), Caller{Agent: "ada"}, "inbox-a", "http://127.0.0.1/hook"); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
}

type countingLog struct {
	store.Log
	since int
}

func (c *countingLog) Since(ctx context.Context, inbox string, after int64, limit int) ([]store.Record, error) {
	c.since++
	return c.Log.Since(ctx, inbox, after, limit)
}

type eventMail struct {
	state string
	next  mailbackend.Delta
}

func (eventMail) CreateInbox(context.Context, mailbackend.InboxSpec) (string, error) {
	return "", nil
}
func (eventMail) CreateInboxes(context.Context, []mailbackend.InboxSpec) ([]string, error) {
	return nil, nil
}
func (eventMail) Deliver(context.Context, mailbackend.Outbound) error { return nil }
func (eventMail) List(context.Context, string) ([]mailbackend.Message, error) {
	return nil, nil
}
func (eventMail) Search(context.Context, string, string) ([]mailbackend.Message, error) {
	return nil, nil
}
func (eventMail) Get(context.Context, string, string) (mailbackend.Message, error) {
	return mailbackend.Message{}, nil
}
func (eventMail) Delete(context.Context, string, string) error { return nil }
func (m *eventMail) MailState(context.Context, string) (string, error) {
	return m.state, nil
}
func (m *eventMail) MailChanges(_ context.Context, _, since string) (mailbackend.Delta, error) {
	if since == m.state {
		return m.next, nil
	}
	return mailbackend.Delta{State: since}, nil
}

func TestEventsWaitUntilTheDeadline(t *testing.T) {
	svc := New(&eventMail{})
	svc.SetFeed(events.New(&store.Memory{}))
	svc.Allow("ada", "inbox-a", PermRead)
	start := time.Now()
	records, err := svc.Events(context.Background(), Caller{Agent: "ada"}, "inbox-a", 0, 30*time.Millisecond)
	if err != nil || len(records) != 0 || time.Since(start) < 20*time.Millisecond {
		t.Fatalf("wait %v %+v after %s", err, records, time.Since(start))
	}
}
