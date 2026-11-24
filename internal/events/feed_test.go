// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestSyncSurvivesANewFeed(t *testing.T) {
	log := &store.Memory{}
	watch := &fakeWatch{state: "s0", next: mailbackend.Delta{State: "s1", Created: []string{"m1"}}}
	first := New(log)
	if err := first.Remember(context.Background(), "inbox-a", "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := first.Sync(context.Background(), watch, "inbox-a"); err != nil {
		t.Fatal(err)
	}
	if err := first.Sync(context.Background(), watch, "inbox-a"); err != nil {
		t.Fatal(err)
	}
	second := New(log)
	records, err := second.Since(context.Background(), "inbox-a", 0, 10)
	if err != nil || len(records) != 1 || records[0].MessageID != "m1" || records[0].Kind != "created" {
		t.Fatalf("records %+v err %v", records, err)
	}
	inbox, ok, err := second.InboxByAddress(context.Background(), "Ada@example.com")
	if err != nil || !ok || inbox != "inbox-a" {
		t.Fatalf("address %s %v %v", inbox, ok, err)
	}
}

func TestWebhookRetriesThenSignsTheBody(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var sigs []string
	fails := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		sigs = append(sigs, r.Header.Get("X-Inbai-Signature"))
		fails++
		attempt := fails
		mu.Unlock()
		if attempt == 1 {
			http.Error(w, "later", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	log := &store.Memory{}
	feed := New(log)
	feed.backoff = func(int) time.Duration { return 0 }
	_, secret, err := feed.Subscribe(context.Background(), "ada", "inbox-a", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Put(context.Background(), "inbox-a", "", "s1", []store.Record{{MessageID: "m1", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := feed.DeliverOnce(context.Background(), srv.Client()); !ok || err != nil {
		t.Fatalf("first %v %v", ok, err)
	}
	if ok, err := feed.DeliverOnce(context.Background(), srv.Client()); !ok || err != nil {
		t.Fatalf("second %v %v", ok, err)
	}
	if ok, err := feed.DeliverOnce(context.Background(), srv.Client()); ok || err != nil {
		t.Fatalf("third should be idle, ok %v err %v", ok, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || !strings.Contains(bodies[0], `"messageId":"m1"`) || !ValidSignature(secret, []byte(bodies[1]), sigs[1]) {
		t.Fatalf("bodies %v sigs %v", bodies, sigs)
	}
}

func TestClaimLeaseStopsASecondWorker(t *testing.T) {
	log := &store.Memory{}
	if err := log.Subscribe(context.Background(), store.Subscription{ID: "sub", Inbox: "inbox-a", URL: "http://example", Secret: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := log.Put(context.Background(), "inbox-a", "", "s1", []store.Record{{MessageID: "m", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first, ok, err := log.Claim(context.Background(), now)
	if err != nil || !ok {
		t.Fatal(err)
	}
	_, ok, err = log.Claim(context.Background(), now)
	if err != nil || ok {
		t.Fatalf("second claim ok %v err %v", ok, err)
	}
	if _, ok, err = log.Claim(context.Background(), now.Add(31*time.Second)); err != nil || !ok {
		t.Fatalf("lease should expire, ok %v err %v", ok, err)
	}
	if first.Event.MessageID != "m" {
		t.Fatalf("event %+v", first.Event)
	}
}

func TestSweepContinuesPastAFailingInbox(t *testing.T) {
	ctx := context.Background()
	log := &store.Memory{}
	feed := New(log)
	for _, inbox := range []string{"inbox-a", "inbox-b", "inbox-c"} {
		if err := feed.Remember(ctx, inbox, inbox+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	gone := errors.New("account not found")
	watch := &failingWatch{fail: map[string]error{"inbox-a": gone}}
	err := feed.Sweep(ctx, watch)
	if !errors.Is(err, gone) || !strings.Contains(err.Error(), "inbox-a") {
		t.Fatalf("sweep err %v", err)
	}
	for _, inbox := range []string{"inbox-b", "inbox-c"} {
		state, ok, err := log.State(ctx, inbox)
		if err != nil || !ok || state != "s0" {
			t.Fatalf("%s state %q ok %v err %v", inbox, state, ok, err)
		}
	}
	if strings.Contains(err.Error(), "inbox-b") || strings.Contains(err.Error(), "inbox-c") {
		t.Fatalf("sweep err names a healthy inbox: %v", err)
	}
}

func TestSweepStopsWhenTheContextEnds(t *testing.T) {
	log := &store.Memory{}
	feed := New(log)
	if err := feed.Remember(context.Background(), "inbox-a", "a@example.com"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	watch := &failingWatch{}
	if err := feed.Sweep(ctx, watch); !errors.Is(err, context.Canceled) {
		t.Fatalf("sweep err %v", err)
	}
	if len(watch.asked) != 0 {
		t.Fatalf("asked %v after cancel", watch.asked)
	}
}

func TestForgetStopsTheSweep(t *testing.T) {
	ctx := context.Background()
	log := &store.Memory{}
	feed := New(log)
	if err := feed.Remember(ctx, "inbox-a", "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := feed.Forget(ctx, "inbox-a"); err != nil {
		t.Fatal(err)
	}
	watch := &failingWatch{}
	if err := feed.Sweep(ctx, watch); err != nil || len(watch.asked) != 0 {
		t.Fatalf("sweep err %v asked %v", err, watch.asked)
	}
}

// failingWatch returns state s0 for every inbox except those listed in fail.
type failingWatch struct {
	mu    sync.Mutex
	fail  map[string]error
	asked []string
}

func (f *failingWatch) MailState(_ context.Context, inbox string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, inbox)
	if err := f.fail[inbox]; err != nil {
		return "", err
	}
	return "s0", nil
}

func (f *failingWatch) MailChanges(_ context.Context, _, since string) (mailbackend.Delta, error) {
	return mailbackend.Delta{State: since}, nil
}

type fakeWatch struct {
	state string
	next  mailbackend.Delta
}

func (f *fakeWatch) MailState(context.Context, string) (string, error) { return f.state, nil }

func (f *fakeWatch) MailChanges(_ context.Context, _, since string) (mailbackend.Delta, error) {
	if since == f.state {
		return f.next, nil
	}
	return mailbackend.Delta{State: since}, nil
}
