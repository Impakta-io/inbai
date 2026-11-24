// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/store"
)

// runWebhooks starts the delivery loop and returns a stop that waits for it to return.
func runWebhooks(feed *Feed, client *http.Client) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		feed.RunWebhooks(ctx, client)
		close(done)
	}()
	return func() {
		cancel()
		<-done
	}
}

func putEvents(t *testing.T, log store.Log, inbox string, ids ...string) {
	t.Helper()
	records := make([]store.Record, len(ids))
	for i, id := range ids {
		records[i] = store.Record{MessageID: id, Kind: "created"}
	}
	if err := log.Put(context.Background(), inbox, "", "s-"+ids[len(ids)-1], records); err != nil {
		t.Fatal(err)
	}
}

func TestSlowReceiverDoesNotDelayAnotherSubscription(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	var slowHits atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slowHits.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	fast := make(chan string, 10)
	quick := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		fast <- string(raw)
		w.WriteHeader(http.StatusNoContent)
	}))
	log := &store.Memory{}
	feed := New(log)
	if _, _, err := feed.Subscribe(ctx, store.Subscription{Agent: "ada", Inbox: "inbox-a", URL: slow.URL}, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := feed.Subscribe(ctx, store.Subscription{Agent: "bob", Inbox: "inbox-b", URL: quick.URL}, 0); err != nil {
		t.Fatal(err)
	}
	// The slow receiver's events are older, so they are claimed first.
	putEvents(t, log, "inbox-a", "a1", "a2", "a3")
	putEvents(t, log, "inbox-b", "b1")
	stop := runWebhooks(feed, slow.Client())
	defer func() {
		stop()
		close(release)
		slow.Close()
		quick.Close()
	}()

	select {
	case body := <-fast:
		if !strings.Contains(body, `"messageId":"b1"`) {
			t.Fatalf("fast receiver got %s", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a receiver that does not answer held back another subscription's delivery")
	}
	if hits := slowHits.Load(); hits > 1 {
		t.Fatalf("slow receiver had %d requests in flight", hits)
	}
	snap, err := log.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range snap.Deliveries {
		if item.EventID > 1 && item.EventID < 4 && item.Attempt != 0 {
			t.Fatalf("a later delivery to the busy webhook was claimed: %+v", item)
		}
	}
}

func TestPoolKeepsPerSubscriptionOrder(t *testing.T) {
	ctx := context.Background()
	const perSub = 10
	var mu sync.Mutex
	order := map[string][]int64{}
	inflight := map[string]int{}
	most := map[string]int{}
	bSeen := make(chan struct{})
	var bOnce sync.Once
	overlap := false
	finished := make(chan struct{})
	var finishOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := strings.TrimPrefix(r.URL.Path, "/")
		var event store.Record
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &event)
		mu.Lock()
		inflight[sub]++
		most[sub] = max(most[sub], inflight[sub])
		first := len(order[sub]) == 0
		mu.Unlock()
		if sub == "b" {
			bOnce.Do(func() { close(bSeen) })
		}
		if sub == "a" && first {
			// The first post to a waits until b is being posted too, which only a pool can do.
			select {
			case <-bSeen:
				mu.Lock()
				overlap = true
				mu.Unlock()
			case <-time.After(3 * time.Second):
			}
		}
		mu.Lock()
		inflight[sub]--
		order[sub] = append(order[sub], event.ID)
		if len(order["a"]) == perSub && len(order["b"]) == perSub {
			finishOnce.Do(func() { close(finished) })
		}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	log := &store.Memory{}
	feed := New(log)
	for _, sub := range []string{"a", "b"} {
		if _, _, err := feed.Subscribe(ctx, store.Subscription{Agent: sub, Inbox: "inbox-" + sub, URL: srv.URL + "/" + sub}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, sub := range []string{"a", "b"} {
		ids := make([]string, perSub)
		for i := range ids {
			ids[i] = fmt.Sprintf("%s%d", sub, i)
		}
		putEvents(t, log, "inbox-"+sub, ids...)
	}
	stop := runWebhooks(feed, srv.Client())
	defer stop()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("not every event was delivered: %v", order)
	}
	mu.Lock()
	defer mu.Unlock()
	if !overlap {
		t.Fatal("two webhooks were never posted at the same time")
	}
	for sub, ids := range order {
		if most[sub] != 1 {
			t.Fatalf("%s had %d posts in flight", sub, most[sub])
		}
		for i := 1; i < len(ids); i++ {
			if ids[i] <= ids[i-1] {
				t.Fatalf("%s got events out of order: %v", sub, ids)
			}
		}
	}
}

func TestRetriesFollowTheScheduleAndSurviveARestart(t *testing.T) {
	ctx := context.Background()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	log := &store.Memory{}
	now := time.Now().Add(time.Second)
	clock := func() time.Time { return now }
	feed := New(log)
	feed.now = clock
	if _, _, err := feed.Subscribe(ctx, store.Subscription{Agent: "ada", Inbox: "inbox-a", URL: srv.URL}, 0); err != nil {
		t.Fatal(err)
	}
	putEvents(t, log, "inbox-a", "m1")
	waits := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}
	for i, wait := range waits {
		if ok, err := feed.DeliverOnce(ctx, srv.Client()); !ok || err != nil {
			t.Fatalf("attempt %d ok %v err %v", i+1, ok, err)
		}
		snap, err := log.Export(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := snap.Deliveries[0].Next; !got.Equal(now.Add(wait)) {
			t.Fatalf("after attempt %d the next one is at +%s, want +%s", i+1, got.Sub(now), wait)
		}
		// A new feed over the same log is a restarted process. It must not hurry the retry.
		feed = New(log)
		feed.now = clock
		now = now.Add(wait - time.Second)
		if ok, err := feed.DeliverOnce(ctx, srv.Client()); ok || err != nil {
			t.Fatalf("attempt %d was retried before its time, ok %v err %v", i+1, ok, err)
		}
		now = now.Add(time.Second)
	}
	if ok, err := feed.DeliverOnce(ctx, srv.Client()); !ok || err != nil {
		t.Fatalf("last attempt ok %v err %v", ok, err)
	}
	now = now.Add(48 * time.Hour)
	if ok, err := feed.DeliverOnce(ctx, srv.Client()); ok || err != nil {
		t.Fatalf("a delivery was tried after the last attempt, ok %v err %v", ok, err)
	}
	snap, err := log.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != int32(len(waits)+1) || !snap.Deliveries[0].Failed {
		t.Fatalf("hits %d delivery %+v", hits.Load(), snap.Deliveries[0])
	}
}
