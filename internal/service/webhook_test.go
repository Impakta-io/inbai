// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/store"
)

// The same webhook lifecycle runs on the in-memory store and on PostgreSQL.
var webhookLifecycle = []struct {
	name string
	run  func(*testing.T, hookEnv)
}{
	{"RevokeDropsQueuedAndFutureDeliveries", checkRevokeStopsWebhook},
	{"SuspendDropsDeliveries", checkSuspendStopsWebhook},
	{"DeleteStopsDeliveries", checkDeleteStopsWebhook},
	{"OtherAgentCannotListOrDelete", checkWebhookOwnership},
	{"OperatorWebhookIsNotGated", checkOperatorWebhookIsDelivered},
	{"CapPerAgentPerInbox", checkWebhookCap},
}

func TestWebhookLifecycleInMemory(t *testing.T) {
	for _, tc := range webhookLifecycle {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, memoryHookEnv()) })
	}
}

func TestWebhookLifecycleInPostgres(t *testing.T) {
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	if dsn == "" {
		t.Skip("INBAI_TEST_DATABASE is not set")
	}
	for _, tc := range webhookLifecycle {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, postgresHookEnv(t, dsn)) })
	}
}

type hookEnv struct {
	svc     *Service
	feed    *events.Feed
	log     store.Log
	suspend func(t *testing.T, agent string)
	// name makes agent and inbox names unique, so rows left by an earlier run do not match.
	name func(string) string
}

// directorySuspend suspends an agent through auth.Directory, the source the service reads.
func directorySuspend(dir *auth.Directory) func(*testing.T, string) {
	return func(t *testing.T, agent string) {
		t.Helper()
		if _, _, err := dir.Create(agent, false); err != nil {
			t.Fatal(err)
		}
		if found, err := dir.Suspend(agent); err != nil || !found {
			t.Fatalf("suspend found %v err %v", found, err)
		}
	}
}

func memoryHookEnv() hookEnv {
	log := &store.Memory{}
	feed := events.New(log)
	feed.SetAllowPrivate(true)
	svc := New(&eventMail{})
	svc.SetFeed(feed)
	dir := auth.NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	svc.SetSuspensions(dir)
	return hookEnv{
		svc: svc, feed: feed, log: log,
		suspend: directorySuspend(dir),
		name:    func(s string) string { return s },
	}
}

func postgresHookEnv(t *testing.T, dsn string) hookEnv {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`)
		conn.Release()
	})
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	// Claim takes any due delivery, so clear what other tests queued.
	if _, err := pool.Exec(ctx, `TRUNCATE webhook_delivery, webhook_subscription`); err != nil {
		t.Fatal(err)
	}
	feed := events.New(db)
	feed.SetAllowPrivate(true)
	svc := New(&eventMail{})
	svc.SetDB(db)
	svc.SetFeed(feed)
	dir := auth.NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	dir.SetDB(db)
	svc.SetSuspensions(dir)
	suffix := "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	return hookEnv{
		svc: svc, feed: feed, log: db,
		suspend: directorySuspend(dir),
		name:    func(s string) string { return s + suffix },
	}
}

// hookTarget records every webhook body it receives.
type hookTarget struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits []string
}

func newHookTarget(t *testing.T) *hookTarget {
	t.Helper()
	h := &hookTarget{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.hits = append(h.hits, string(raw))
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hookTarget) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.hits)
}

var opCaller = Caller{Operator: true}

func (e hookEnv) grant(t *testing.T, agent, inbox string) {
	t.Helper()
	if err := e.svc.Grant(opCaller, agent, inbox, PermRead); err != nil {
		t.Fatal(err)
	}
}

func (e hookEnv) put(t *testing.T, inbox, messageID string) {
	t.Helper()
	if err := e.log.Put(context.Background(), inbox, "", "s", []store.Record{{MessageID: messageID, Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
}

// deliver runs one pass of the delivery loop and reports whether a delivery was claimed.
func (e hookEnv) deliver(t *testing.T) bool {
	t.Helper()
	claimed, err := e.feed.DeliverOnce(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return claimed
}

func (e hookEnv) subscribe(t *testing.T, caller Caller, inbox string, target *hookTarget) string {
	t.Helper()
	id, secret, err := e.svc.SubscribeWebhook(context.Background(), caller, inbox, target.srv.URL)
	if err != nil || id == "" || secret == "" {
		t.Fatalf("subscribe id %q err %v", id, err)
	}
	return id
}

func (e hookEnv) hooks(t *testing.T, caller Caller, inbox string) []Webhook {
	t.Helper()
	list, err := e.svc.ListWebhooks(context.Background(), caller, inbox)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func checkRevokeStopsWebhook(t *testing.T, env hookEnv) {
	ada, inbox := env.name("ada"), env.name("inbox-a")
	target := newHookTarget(t)
	env.grant(t, ada, inbox)
	env.subscribe(t, Caller{Agent: ada}, inbox, target)
	env.put(t, inbox, "m1")
	if !env.deliver(t) || target.count() != 1 {
		t.Fatalf("delivery before revoke: hits %d", target.count())
	}
	// m2 is queued while ada still holds read. Revoking afterwards must still stop it.
	env.put(t, inbox, "m2")
	if err := env.svc.Revoke(opCaller, ada, inbox, PermRead); err != nil {
		t.Fatal(err)
	}
	if !env.deliver(t) {
		t.Fatal("the queued delivery was not claimed")
	}
	if target.count() != 1 {
		t.Fatalf("a delivery was sent after read was revoked: %v", target.hits)
	}
	list := env.hooks(t, Caller{Agent: ada}, inbox)
	if len(list) != 1 || list[0].Active || list[0].DisabledReason != "read grant revoked" {
		t.Fatalf("webhook after revoke %+v", list)
	}
	env.put(t, inbox, "m3")
	if env.deliver(t) {
		t.Fatal("a disabled webhook was queued a delivery")
	}
	// Access returning does not turn the webhook back on.
	env.grant(t, ada, inbox)
	env.put(t, inbox, "m4")
	if env.deliver(t) || target.count() != 1 {
		t.Fatalf("disabled webhook resumed: hits %d", target.count())
	}
	if list := env.hooks(t, Caller{Agent: ada}, inbox); len(list) != 1 || list[0].Active {
		t.Fatalf("webhook after grant returned %+v", list)
	}
}

func checkSuspendStopsWebhook(t *testing.T, env hookEnv) {
	ada, inbox := env.name("ada"), env.name("inbox-a")
	target := newHookTarget(t)
	env.grant(t, ada, inbox)
	env.subscribe(t, Caller{Agent: ada}, inbox, target)
	env.put(t, inbox, "m1")
	if !env.deliver(t) || target.count() != 1 {
		t.Fatalf("delivery before suspend: hits %d", target.count())
	}
	env.put(t, inbox, "m2")
	env.suspend(t, ada)
	if !env.deliver(t) {
		t.Fatal("the queued delivery was not claimed")
	}
	if target.count() != 1 {
		t.Fatalf("a delivery was sent to a suspended agent: %v", target.hits)
	}
	list := env.hooks(t, opCaller, inbox)
	if len(list) != 1 || list[0].Active || list[0].DisabledReason != "agent suspended" {
		t.Fatalf("webhook after suspend %+v", list)
	}
	env.put(t, inbox, "m3")
	if env.deliver(t) {
		t.Fatal("a disabled webhook was queued a delivery")
	}
}

func checkDeleteStopsWebhook(t *testing.T, env hookEnv) {
	ada, inbox := env.name("ada"), env.name("inbox-a")
	target := newHookTarget(t)
	env.grant(t, ada, inbox)
	id := env.subscribe(t, Caller{Agent: ada}, inbox, target)
	env.put(t, inbox, "m1")
	if err := env.svc.DeleteWebhook(context.Background(), Caller{Agent: ada}, id); err != nil {
		t.Fatal(err)
	}
	if env.deliver(t) || target.count() != 0 {
		t.Fatalf("a delivery queued before delete was sent: hits %d", target.count())
	}
	env.put(t, inbox, "m2")
	if env.deliver(t) || target.count() != 0 {
		t.Fatalf("a deleted webhook got a delivery: hits %d", target.count())
	}
	if list := env.hooks(t, opCaller, inbox); len(list) != 0 {
		t.Fatalf("deleted webhook is listed %+v", list)
	}
	if err := env.svc.DeleteWebhook(context.Background(), Caller{Agent: ada}, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete %v", err)
	}
}

func checkWebhookOwnership(t *testing.T, env hookEnv) {
	ctx := context.Background()
	ada, bob, inbox := env.name("ada"), env.name("bob"), env.name("inbox-a")
	target := newHookTarget(t)
	env.grant(t, ada, inbox)
	env.grant(t, bob, inbox)
	adaHook := env.subscribe(t, Caller{Agent: ada}, inbox, target)
	bobHook := env.subscribe(t, Caller{Agent: bob}, inbox, target)
	opHook := env.subscribe(t, opCaller, inbox, target)

	list := env.hooks(t, Caller{Agent: bob}, inbox)
	if len(list) != 1 || list[0].ID != bobHook || list[0].Agent != bob || !list[0].Active {
		t.Fatalf("bob sees %+v", list)
	}
	if list := env.hooks(t, Caller{Agent: env.name("eve")}, inbox); len(list) != 0 {
		t.Fatalf("an agent with no webhooks sees %+v", list)
	}
	if list := env.hooks(t, opCaller, inbox); len(list) != 3 {
		t.Fatalf("operator sees %+v", list)
	}
	for _, id := range []string{adaHook, opHook, "missing"} {
		if err := env.svc.DeleteWebhook(ctx, Caller{Agent: bob}, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("bob deleting %s: %v", id, err)
		}
	}
	if list := env.hooks(t, Caller{Agent: ada}, inbox); len(list) != 1 || list[0].ID != adaHook {
		t.Fatalf("ada's webhook after bob's delete %+v", list)
	}
	// The webhook bob could not delete still delivers.
	env.put(t, inbox, "m1")
	for range 3 {
		env.deliver(t)
	}
	if target.count() != 3 {
		t.Fatalf("hits %d, want one per webhook", target.count())
	}
	if err := env.svc.DeleteWebhook(ctx, opCaller, adaHook); err != nil {
		t.Fatalf("operator delete %v", err)
	}
	if _, err := env.svc.ListWebhooks(ctx, Caller{}, inbox); !errors.Is(err, ErrForbidden) {
		t.Fatalf("anonymous list %v", err)
	}
	if _, err := env.svc.ListWebhooks(ctx, opCaller, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("list without an inbox %v", err)
	}
}

func checkOperatorWebhookIsDelivered(t *testing.T, env hookEnv) {
	inbox := env.name("inbox-a")
	target := newHookTarget(t)
	env.subscribe(t, opCaller, inbox, target)
	env.put(t, inbox, "m1")
	if !env.deliver(t) || target.count() != 1 {
		t.Fatalf("operator webhook hits %d", target.count())
	}
	if list := env.hooks(t, opCaller, inbox); len(list) != 1 || !list[0].Active || list[0].Agent != "operator" {
		t.Fatalf("operator webhook %+v", list)
	}
}

func checkWebhookCap(t *testing.T, env hookEnv) {
	ctx := context.Background()
	ada, bob, inbox, other := env.name("ada"), env.name("bob"), env.name("inbox-a"), env.name("inbox-b")
	target := newHookTarget(t)
	env.grant(t, ada, inbox)
	env.grant(t, ada, other)
	env.grant(t, bob, inbox)
	var first string
	for i := range MaxWebhooksPerInbox {
		id := env.subscribe(t, Caller{Agent: ada}, inbox, target)
		if i == 0 {
			first = id
		}
	}
	_, _, err := env.svc.SubscribeWebhook(ctx, Caller{Agent: ada}, inbox, target.srv.URL)
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("webhook over the cap: %v", err)
	}
	if list := env.hooks(t, Caller{Agent: ada}, inbox); len(list) != MaxWebhooksPerInbox {
		t.Fatalf("ada holds %d webhooks", len(list))
	}
	env.subscribe(t, Caller{Agent: ada}, other, target)
	env.subscribe(t, Caller{Agent: bob}, inbox, target)
	env.subscribe(t, opCaller, inbox, target)
	if err := env.svc.DeleteWebhook(ctx, Caller{Agent: ada}, first); err != nil {
		t.Fatal(err)
	}
	env.subscribe(t, Caller{Agent: ada}, inbox, target)
}

// unreadableSuspensions fails like auth.Directory does when its store cannot be read.
type unreadableSuspensions struct{ err error }

func (u unreadableSuspensions) Suspended(string) (bool, error) { return false, u.err }

func TestWebhookIsNotSentWhenSuspensionCannotBeRead(t *testing.T) {
	env := memoryHookEnv()
	down := fmt.Errorf("%w: connection refused", auth.ErrUnavailable)
	env.svc.SetSuspensions(unreadableSuspensions{err: down})
	target := newHookTarget(t)
	env.grant(t, "ada", "inbox-a")
	env.subscribe(t, Caller{Agent: "ada"}, "inbox-a", target)
	env.put(t, "inbox-a", "m1")
	claimed, err := env.feed.DeliverOnce(context.Background(), nil)
	if !claimed || !errors.Is(err, auth.ErrUnavailable) || target.count() != 0 {
		t.Fatalf("claimed %v err %v hits %d", claimed, err, target.count())
	}
	// Not knowing is not a refusal: the webhook stays active and is retried.
	if list := env.hooks(t, Caller{Agent: "ada"}, "inbox-a"); len(list) != 1 || !list[0].Active {
		t.Fatalf("webhook after a read error %+v", list)
	}
}
