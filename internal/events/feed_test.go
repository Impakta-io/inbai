// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	_, secret, err := feed.Subscribe(context.Background(), store.Subscription{Agent: "ada", Inbox: "inbox-a", URL: srv.URL}, 0)
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
	if err := log.Subscribe(context.Background(), store.Subscription{ID: "sub", Inbox: "inbox-a", URL: "http://example", Secret: "s"}, 0); err != nil {
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

func TestSyncForgetsAnInboxTheServerNoLongerHas(t *testing.T) {
	ctx := context.Background()
	log := &store.Memory{}
	feed, notices := quietFeed(log)
	if err := feed.Remember(ctx, "inbox-a", "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := log.Put(ctx, "inbox-a", "", "s0", nil); err != nil {
		t.Fatal(err)
	}
	watch := &checkedWatch{lostWatch: lostWatch{current: "s0", lost: map[string]bool{"s0": true}}, exists: false}
	if err := feed.Sweep(ctx, watch); err != nil {
		t.Fatalf("sweep err %v", err)
	}
	if _, ok, err := log.State(ctx, "inbox-a"); err != nil || ok {
		t.Fatalf("cursor kept ok %v err %v", ok, err)
	}
	if _, ok, err := feed.InboxByAddress(ctx, "a@example.com"); err != nil || ok {
		t.Fatalf("address kept ok %v err %v", ok, err)
	}
	if records, err := feed.Since(ctx, "inbox-a", 0, 10); err != nil || len(records) != 0 {
		t.Fatalf("records %+v err %v", records, err)
	}
	if !strings.Contains(notices.String(), "inbox=inbox-a") || strings.Count(notices.String(), "\n") != 1 {
		t.Fatalf("notices %q", notices.String())
	}
	asked := len(watch.since)
	if err := feed.Sweep(ctx, watch); err != nil || len(watch.since) != asked || watch.checked != 1 {
		t.Fatalf("second sweep err %v since %v checked %d", err, watch.since, watch.checked)
	}
	if strings.Count(notices.String(), "\n") != 1 {
		t.Fatalf("logged again %q", notices.String())
	}
}

func TestSyncResyncsWhenTheServerLostTheChanges(t *testing.T) {
	ctx := context.Background()
	log := &store.Memory{}
	feed, notices := quietFeed(log)
	if err := log.Put(ctx, "inbox-a", "", "s0", nil); err != nil {
		t.Fatal(err)
	}
	watch := &checkedWatch{lostWatch: lostWatch{current: "s9", lost: map[string]bool{"s0": true}}, exists: true}
	if err := feed.Sweep(ctx, watch); err != nil {
		t.Fatalf("sweep err %v", err)
	}
	state, ok, err := log.State(ctx, "inbox-a")
	if err != nil || !ok || state != "s9" {
		t.Fatalf("state %q ok %v err %v", state, ok, err)
	}
	records, err := feed.Since(ctx, "inbox-a", 0, 10)
	if err != nil || len(records) != 1 || records[0].Kind != "resync" || records[0].MessageID != "" {
		t.Fatalf("records %+v err %v", records, err)
	}
	if !strings.Contains(notices.String(), "inbox=inbox-a") || !strings.Contains(notices.String(), "from=s0 to=s9") {
		t.Fatalf("notices %q", notices.String())
	}
	if err := feed.Sweep(ctx, watch); err != nil {
		t.Fatalf("second sweep err %v", err)
	}
	if got := strings.Join(watch.since, ","); got != "s0,s9" {
		t.Fatalf("asked since %s", got)
	}
	if again, _ := feed.Since(ctx, "inbox-a", 0, 10); len(again) != 1 {
		t.Fatalf("records after a healthy sweep %+v", again)
	}
}

func TestResyncIsPostedToEachWebhook(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
	}))
	defer srv.Close()
	log := &store.Memory{}
	feed, _ := quietFeed(log)
	if err := log.Put(ctx, "inbox-a", "", "s0", nil); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"ada", "bob"} {
		if _, _, err := feed.Subscribe(ctx, store.Subscription{Agent: agent, Inbox: "inbox-a", URL: srv.URL}, 0); err != nil {
			t.Fatal(err)
		}
	}
	watch := &checkedWatch{lostWatch: lostWatch{current: "s9", lost: map[string]bool{"s0": true}}, exists: true}
	if err := feed.Sync(ctx, watch, "inbox-a"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := feed.DeliverOnce(ctx, srv.Client()); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := `{"id":1,"inbox":"inbox-a","messageId":"","kind":"resync"}`
	if len(bodies) != 2 || bodies[0] != want || bodies[1] != want {
		t.Fatalf("bodies %q", bodies)
	}
}

func TestSyncResyncsAWatcherThatCannotCheckTheInbox(t *testing.T) {
	ctx := context.Background()
	log := &store.Memory{}
	feed, _ := quietFeed(log)
	if err := log.Put(ctx, "inbox-a", "", "4:8", nil); err != nil {
		t.Fatal(err)
	}
	watch := &lostWatch{current: "5:1", lost: map[string]bool{"4:8": true}}
	if err := feed.Sync(ctx, watch, "inbox-a"); err != nil {
		t.Fatalf("sync err %v", err)
	}
	state, _, _ := log.State(ctx, "inbox-a")
	records, _ := feed.Since(ctx, "inbox-a", 0, 10)
	if state != "5:1" || len(records) != 1 || records[0].Kind != "resync" {
		t.Fatalf("state %q records %+v", state, records)
	}
}

func TestSyncKeepsTheCursorWhenRecoveryCannotHelp(t *testing.T) {
	broken := errors.New("connection refused")
	for name, watch := range map[string]*checkedWatch{
		"other changes error": {lostWatch: lostWatch{current: "s9", fail: broken}, exists: true},
		"check fails":         {lostWatch: lostWatch{current: "s9", lost: map[string]bool{"s0": true}}, err: broken},
		"state fails":         {lostWatch: lostWatch{stateErr: broken, lost: map[string]bool{"s0": true}}, exists: true},
		"state did not move":  {lostWatch: lostWatch{current: "s0", lost: map[string]bool{"s0": true}}, exists: true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			log := &store.Memory{}
			feed, notices := quietFeed(log)
			if err := log.Put(ctx, "inbox-a", "", "s0", nil); err != nil {
				t.Fatal(err)
			}
			err := feed.Sweep(ctx, watch)
			if err == nil || !strings.Contains(err.Error(), "inbox-a") {
				t.Fatalf("sweep err %v", err)
			}
			if watch.fail != nil && watch.checked != 0 {
				t.Fatalf("checked the inbox on an unrelated error")
			}
			state, ok, _ := log.State(ctx, "inbox-a")
			records, _ := feed.Since(ctx, "inbox-a", 0, 10)
			if !ok || state != "s0" || len(records) != 0 || notices.Len() != 0 {
				t.Fatalf("state %q ok %v records %+v notices %q", state, ok, records, notices.String())
			}
		})
	}
}

func quietFeed(log store.Log) (*Feed, *bytes.Buffer) {
	var notices bytes.Buffer
	feed := New(log)
	feed.logger = slog.New(slog.NewTextHandler(&notices, nil))
	return feed, &notices
}

// lostWatch fails MailChanges with ErrChangesLost for each state in lost,
// and with fail when it is set. Other states report no change.
type lostWatch struct {
	current  string
	stateErr error
	lost     map[string]bool
	fail     error
	since    []string
}

func (w *lostWatch) MailState(context.Context, string) (string, error) {
	return w.current, w.stateErr
}

func (w *lostWatch) MailChanges(_ context.Context, _, since string) (mailbackend.Delta, error) {
	w.since = append(w.since, since)
	if w.fail != nil {
		return mailbackend.Delta{}, w.fail
	}
	if w.lost[since] {
		return mailbackend.Delta{}, fmt.Errorf("stalwart c: cannotCalculateChanges: %w", mailbackend.ErrChangesLost)
	}
	return mailbackend.Delta{State: since}, nil
}

// checkedWatch is a lostWatch that can also say whether the inbox exists.
type checkedWatch struct {
	lostWatch
	exists  bool
	err     error
	checked int
}

func (w *checkedWatch) InboxExists(context.Context, string) (bool, error) {
	w.checked++
	return w.exists, w.err
}
