// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/store"
)

// gateMail is a backend whose Deliver waits on release when gated, so a test can hold a send in flight.
type gateMail struct {
	*fakeMail
	mu        sync.Mutex
	gated     bool
	entered   chan struct{}
	release   chan struct{}
	delivered []mailbackend.Outbound
	failNext  int
}

func newGateMail() *gateMail {
	return &gateMail{
		fakeMail: &fakeMail{created: "id-1", messages: []mailbackend.Message{{ID: "m", Subject: "hello", From: "ada@sender.test", MessageID: []string{"<m@sender.test>"}}}},
		entered:  make(chan struct{}, 64),
		release:  make(chan struct{}),
	}
}

func (g *gateMail) Deliver(_ context.Context, msg mailbackend.Outbound) error {
	g.mu.Lock()
	gated := g.gated
	g.mu.Unlock()
	if gated {
		g.entered <- struct{}{}
		<-g.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failNext > 0 {
		g.failNext--
		return errors.New("backend unavailable")
	}
	g.delivered = append(g.delivered, msg)
	return nil
}

func (g *gateMail) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.delivered)
}

// idempotencyBackends runs a test once with keys in memory and once in PostgreSQL
// when INBAI_TEST_DATABASE is set. open returns a fresh service over mail.
func idempotencyBackends(t *testing.T, run func(t *testing.T, open func(mailbackend.Backend) *Service)) {
	t.Run("memory", func(t *testing.T) {
		run(t, New)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("INBAI_TEST_DATABASE")
		if dsn == "" {
			t.Skip("INBAI_TEST_DATABASE is not set")
		}
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`) }()
		db, err := store.Open(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := pool.Exec(ctx, `TRUNCATE idempotency, quota_counter`); err != nil {
			t.Fatal(err)
		}
		run(t, func(mail mailbackend.Backend) *Service {
			svc := New(mail)
			svc.SetDB(db)
			return svc
		})
	})
}

var operator = Caller{Operator: true}

func outbound(subject string) mailbackend.Outbound {
	return mailbackend.Outbound{AccountID: "inbox-a", To: "bob@example.com", Subject: subject, Body: "body"}
}

func TestConcurrentSendsWithOneKeyDeliverOnce(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		mail.gated = true
		svc := open(mail)
		const n = 5
		results := make(chan error, n)
		for range n {
			go func() { results <- svc.Deliver(context.Background(), operator, outbound("hi"), "retry-1") }()
		}
		released := false
		defer func() {
			if !released {
				close(mail.release)
			}
		}()
		select {
		case <-mail.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("no send reached the backend")
		}
		// One send is held in the backend. Every other request must answer without reaching it.
		for range n - 1 {
			select {
			case err := <-results:
				if !errors.Is(err, ErrInProgress) {
					t.Errorf("concurrent repeat err = %v, want ErrInProgress", err)
				}
			case <-mail.entered:
				t.Fatal("a second send with the same key reached the backend")
			case <-time.After(5 * time.Second):
				t.Fatal("a concurrent repeat did not answer")
			}
		}
		close(mail.release)
		released = true
		if err := <-results; err != nil {
			t.Fatalf("held send err = %v", err)
		}
		mail.mu.Lock()
		mail.gated = false
		mail.mu.Unlock()
		if err := svc.Deliver(context.Background(), operator, outbound("hi"), "retry-1"); err != nil {
			t.Fatalf("repeat after done err = %v", err)
		}
		if got := mail.count(); got != 1 {
			t.Fatalf("deliveries = %d, want 1", got)
		}
	})
}

func TestKeyReusedForADifferentRequest(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		svc := open(mail)
		ctx := context.Background()
		if err := svc.Deliver(ctx, operator, outbound("first"), "k"); err != nil {
			t.Fatal(err)
		}
		other := outbound("first")
		other.Attachments = []mailbackend.File{{Name: "a.txt", Type: "text/plain", Data: []byte("x")}}
		for name, msg := range map[string]mailbackend.Outbound{
			"subject":    outbound("second"),
			"attachment": other,
		} {
			if err := svc.Deliver(ctx, operator, msg, "k"); !errors.Is(err, ErrKeyReused) {
				t.Fatalf("different %s err = %v, want ErrKeyReused", name, err)
			}
		}
		if got := mail.count(); got != 1 {
			t.Fatalf("deliveries = %d, want 1", got)
		}
		if _, err := svc.CreateInbox(ctx, operator, mailbackend.InboxSpec{Local: "alice"}, "c"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CreateInbox(ctx, operator, mailbackend.InboxSpec{Local: "alice", QuotaBytes: 1}, "c"); !errors.Is(err, ErrKeyReused) {
			t.Fatalf("create with a different quota err = %v, want ErrKeyReused", err)
		}
		id, err := svc.CreateInbox(ctx, operator, mailbackend.InboxSpec{Local: "alice"}, "c")
		if err != nil || id != "id-1" || mail.creates != 1 {
			t.Fatalf("create repeat id %q err %v creates %d", id, err, mail.creates)
		}
	})
}

func TestSendAndReplyKeysAreSeparate(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		svc := open(mail)
		ctx := context.Background()
		if err := svc.Deliver(ctx, operator, outbound("hi"), "same"); err != nil {
			t.Fatal(err)
		}
		if err := svc.Reply(ctx, operator, "inbox-a", "m", mailbackend.Outbound{Body: "thanks"}, "same"); err != nil {
			t.Fatal(err)
		}
		if err := svc.Reply(ctx, operator, "inbox-a", "m", mailbackend.Outbound{Body: "thanks"}, "same"); err != nil {
			t.Fatal(err)
		}
		if got := mail.count(); got != 2 {
			t.Fatalf("deliveries = %d, want a send and a reply", got)
		}
		if mail.delivered[1].To != "ada@sender.test" {
			t.Fatalf("reply went to %q", mail.delivered[1].To)
		}
	})
}

func TestFailedSendReleasesTheKey(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		mail.failNext = 1
		svc := open(mail)
		ctx := context.Background()
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err == nil {
			t.Fatal("the failed send returned nil")
		}
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err != nil {
			t.Fatalf("retry after a failure err = %v", err)
		}
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err != nil {
			t.Fatalf("repeat after the retry err = %v", err)
		}
		if got := mail.count(); got != 1 {
			t.Fatalf("deliveries = %d, want 1", got)
		}
	})
}

func TestStaleReservationIsTakenOver(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		ctx := context.Background()
		mail := newGateMail()
		svc := open(mail)
		start := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		now := start
		svc.now = func() time.Time { return now }
		msg := outbound("hi")
		msg.From = "inbox-a@example.com"
		key := idempotencyKey(operator, "inbox-a", "k")
		// A process reserved the key and stopped before the send finished.
		if _, _, _, err := svc.reserve(ctx, kindSend, key, sendHash("send", "", msg)); err != nil {
			t.Fatal(err)
		}
		now = start.Add(idempotencyTakeover - time.Minute)
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); !errors.Is(err, ErrInProgress) {
			t.Fatalf("fresh reservation err = %v, want ErrInProgress", err)
		}
		if err := svc.Deliver(ctx, operator, outbound("other"), "k"); !errors.Is(err, ErrKeyReused) {
			t.Fatalf("different request err = %v, want ErrKeyReused", err)
		}
		now = start.Add(idempotencyTakeover + time.Minute)
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err != nil {
			t.Fatalf("takeover err = %v", err)
		}
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err != nil {
			t.Fatalf("repeat after takeover err = %v", err)
		}
		if got := mail.count(); got != 1 {
			t.Fatalf("deliveries = %d, want 1", got)
		}
		// The stopped process cannot drop a key another request took over.
		svc.release(ctx, claim{kind: kindSend, key: key, at: start})
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err != nil || mail.count() != 1 {
			t.Fatalf("after a stale release err = %v deliveries %d", err, mail.count())
		}
	})
}
