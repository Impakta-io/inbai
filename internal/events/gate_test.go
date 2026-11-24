// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/store"
)

type fakeGate struct {
	allow  bool
	err    error
	asked  []string
	reason string
}

func (g *fakeGate) MayDeliver(_ context.Context, agent string, operator bool, inbox string) (bool, string, error) {
	who := agent
	if operator {
		who = "operator:" + agent
	}
	g.asked = append(g.asked, who+" "+inbox)
	return g.allow, g.reason, g.err
}

func TestGateRefusalDropsTheDeliveryAndDisablesTheWebhook(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	ctx := context.Background()
	log := &store.Memory{}
	feed := New(log)
	gate := &fakeGate{reason: "read grant revoked"}
	feed.SetGate(gate)
	id, _, err := feed.Subscribe(ctx, store.Subscription{Agent: "ada", Inbox: "inbox-a", URL: srv.URL}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Put(ctx, "inbox-a", "", "s1", []store.Record{{MessageID: "m1", Kind: "created"}, {MessageID: "m2", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := feed.DeliverOnce(ctx, srv.Client()); !ok || err != nil {
		t.Fatalf("deliver ok %v err %v", ok, err)
	}
	if hits.Load() != 0 || len(gate.asked) != 1 || gate.asked[0] != "ada inbox-a" {
		t.Fatalf("hits %d asked %v", hits.Load(), gate.asked)
	}
	sub, ok, err := feed.Subscription(ctx, id)
	if err != nil || !ok || sub.Disabled != "read grant revoked" {
		t.Fatalf("subscription %+v ok %v err %v", sub, ok, err)
	}
	// The second queued delivery failed with the first, so nothing is left to claim.
	if ok, err := feed.DeliverOnce(ctx, srv.Client()); ok || err != nil {
		t.Fatalf("after refusal ok %v err %v", ok, err)
	}
	snap, err := log.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Deliveries) != 2 {
		t.Fatalf("deliveries %+v", snap.Deliveries)
	}
	for _, item := range snap.Deliveries {
		if !item.Failed || item.Reason != "read grant revoked" {
			t.Fatalf("delivery %+v", item)
		}
	}
}

func TestGateErrorDoesNotSend(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	ctx := context.Background()
	log := &store.Memory{}
	feed := New(log)
	feed.backoff = func(int) time.Duration { return 0 }
	down := errors.New("grants unavailable")
	gate := &fakeGate{err: down}
	feed.SetGate(gate)
	if _, _, err := feed.Subscribe(ctx, store.Subscription{Agent: "ada", Inbox: "inbox-a", URL: srv.URL}, 0); err != nil {
		t.Fatal(err)
	}
	if err := log.Put(ctx, "inbox-a", "", "s1", []store.Record{{MessageID: "m1", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := feed.DeliverOnce(ctx, srv.Client()); !errors.Is(err, down) || hits.Load() != 0 {
		t.Fatalf("err %v hits %d", err, hits.Load())
	}
	gate.err, gate.allow = nil, true
	if ok, err := feed.DeliverOnce(ctx, srv.Client()); !ok || err != nil || hits.Load() != 1 {
		t.Fatalf("retry ok %v err %v hits %d", ok, err, hits.Load())
	}
}
