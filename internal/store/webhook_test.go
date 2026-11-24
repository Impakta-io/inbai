// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestMemorySubscriptionLifecycle(t *testing.T) {
	checkSubscriptionLifecycle(t, &Memory{})
}

func TestPostgresSubscriptionLifecycle(t *testing.T) {
	checkSubscriptionLifecycle(t, openTestDB(t))
}

func openTestDB(t *testing.T) *Postgres {
	t.Helper()
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	if dsn == "" {
		t.Skip("INBAI_TEST_DATABASE is not set")
	}
	ctx := context.Background()
	lockTestDB(t, dsn)
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

type subscriptionLog interface {
	Log
	Export(context.Context) (Snapshot, error)
}

// checkSubscriptionLifecycle covers the cap, listing, disabling, and removal on one store.
func checkSubscriptionLifecycle(t *testing.T, log subscriptionLog) {
	t.Helper()
	ctx := context.Background()
	sub := func(id, agent, inbox string, operator bool) Subscription {
		return Subscription{ID: id, Agent: agent, Inbox: inbox, URL: "https://agent.example/" + id, Secret: "s-" + id, Operator: operator}
	}
	for _, item := range []Subscription{sub("a1", "ada", "inbox-a", false), sub("a2", "ada", "inbox-a", false)} {
		if err := log.Subscribe(ctx, item, 2); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Subscribe(ctx, sub("a3", "ada", "inbox-a", false), 2); !errors.Is(err, ErrSubscriptionLimit) {
		t.Fatalf("third subscription %v", err)
	}
	for _, item := range []Subscription{sub("b1", "ada", "inbox-b", false), sub("o1", "operator", "inbox-a", true)} {
		if err := log.Subscribe(ctx, item, 2); err != nil {
			t.Fatalf("%s: %v", item.ID, err)
		}
	}
	list, err := log.Subscriptions(ctx, "inbox-a")
	if err != nil || len(list) != 3 || list[0].ID != "a1" || list[1].ID != "a2" || list[2].ID != "o1" || !list[2].Operator || list[0].Operator {
		t.Fatalf("subscriptions %+v err %v", list, err)
	}
	if got, ok, err := log.Subscription(ctx, "a3"); err != nil || ok {
		t.Fatalf("refused subscription stored %+v err %v", got, err)
	}

	if err := log.Put(ctx, "inbox-a", "", "s1", []Record{{MessageID: "m1", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := log.Disable(ctx, "a1", "read grant revoked", now); err != nil {
		t.Fatal(err)
	}
	if err := log.Unsubscribe(ctx, "a2"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := log.Subscription(ctx, "a1")
	if err != nil || !ok || got.Disabled != "read grant revoked" {
		t.Fatalf("disabled %+v ok %v err %v", got, ok, err)
	}
	if _, ok, err := log.Subscription(ctx, "a2"); err != nil || ok {
		t.Fatalf("unsubscribed ok %v err %v", ok, err)
	}
	// Only the operator's delivery is left to claim.
	delivery, ok, err := log.Claim(ctx, now)
	if err != nil || !ok || delivery.SubscriptionID != "o1" || !delivery.Operator || delivery.Agent != "operator" {
		t.Fatalf("claim %+v ok %v err %v", delivery, ok, err)
	}
	if err := log.Ack(ctx, delivery.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := log.Claim(ctx, now); err != nil || ok {
		t.Fatalf("a disabled or deleted subscription was claimed ok %v err %v", ok, err)
	}
	if err := log.Put(ctx, "inbox-a", "", "s2", []Record{{MessageID: "m2", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	delivery, ok, err = log.Claim(ctx, later)
	if err != nil || !ok || delivery.SubscriptionID != "o1" {
		t.Fatalf("second claim %+v ok %v err %v", delivery, ok, err)
	}
	if _, ok, err := log.Claim(ctx, later); err != nil || ok {
		t.Fatalf("a disabled subscription was queued a delivery ok %v err %v", ok, err)
	}
	snap, err := log.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkLifecycleSnapshot(t, snap)
}

func checkLifecycleSnapshot(t *testing.T, snap Snapshot) {
	t.Helper()
	if err := snap.check(); err != nil {
		t.Fatalf("snapshot after unsubscribe: %v", err)
	}
	reasons := 0
	for _, item := range snap.Deliveries {
		if item.SubscriptionID == "a2" {
			t.Fatalf("delivery for a deleted subscription remains %+v", item)
		}
		if item.SubscriptionID == "a1" {
			if !item.Failed || item.Reason != "read grant revoked" {
				t.Fatalf("delivery for a disabled subscription %+v", item)
			}
			reasons++
		}
	}
	if reasons != 1 {
		t.Fatalf("disabled deliveries %d", reasons)
	}
}

func TestSnapshotKeepsWebhookOwnerAndStop(t *testing.T) {
	restored := &Memory{}
	if err := restored.Import(context.Background(), webhookBackup(t)); err != nil {
		t.Fatal(err)
	}
	checkRestoredOwners(t, restored)
}

func TestPostgresBackupKeepsWebhookOwnerAndStop(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := db.Import(ctx, webhookBackup(t)); err != nil {
		t.Fatal(err)
	}
	again, err := db.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, item := range again.Deliveries {
		if item.SubscriptionID == "a1" && item.Failed && item.Reason == "agent suspended" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("postgres deliveries %+v", again.Deliveries)
	}
	checkRestoredOwners(t, db)
}

// webhookBackup writes and reads back a backup holding an agent's disabled webhook, an
// operator's webhook, and a webhook from an agent that happens to be named operator.
func webhookBackup(t *testing.T) Snapshot {
	t.Helper()
	ctx := context.Background()
	original := &Memory{}
	if err := original.Subscribe(ctx, Subscription{ID: "a1", Agent: "ada", Inbox: "inbox-a", URL: "https://agent.example/a", Secret: "s"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := original.Subscribe(ctx, Subscription{ID: "o1", Agent: "operator", Inbox: "inbox-a", URL: "https://agent.example/o", Secret: "s", Operator: true}, 0); err != nil {
		t.Fatal(err)
	}
	// An agent that happens to be named operator is still an agent.
	if err := original.Subscribe(ctx, Subscription{ID: "x1", Agent: "operator", Inbox: "inbox-a", URL: "https://agent.example/x", Secret: "s"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := original.Put(ctx, "inbox-a", "", "s1", []Record{{MessageID: "m", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if err := original.Disable(ctx, "a1", "agent suspended", time.Now()); err != nil {
		t.Fatal(err)
	}
	snap, err := original.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Encode(snap)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func checkRestoredOwners(t *testing.T, log Log) {
	t.Helper()
	ctx := context.Background()
	want := map[string]struct {
		operator bool
		disabled string
	}{"a1": {false, "agent suspended"}, "o1": {true, ""}, "x1": {false, ""}}
	for id, w := range want {
		sub, ok, err := log.Subscription(ctx, id)
		if err != nil || !ok || sub.Operator != w.operator || sub.Disabled != w.disabled {
			t.Fatalf("%s restored %+v ok %v err %v", id, sub, ok, err)
		}
	}
	delivery, ok, err := log.Claim(ctx, time.Now().Add(time.Minute))
	if err != nil || !ok || delivery.SubscriptionID == "a1" {
		t.Fatalf("claim after restore %+v ok %v err %v", delivery, ok, err)
	}
}

func TestOldSnapshotSubscriptionGainsAnOwner(t *testing.T) {
	decoded, err := Decode([]byte(`{"version":1,"cursors":[],"events":[{"id":1,"inbox":"inbox-a","messageId":"m","kind":"created"}],
"subscriptions":[{"id":"o1","agent":"operator","inbox":"inbox-a","url":"https://agent.example/o","secret":"s"},
{"id":"a1","agent":"ada","inbox":"inbox-a","url":"https://agent.example/a","secret":"s"}],
"deliveries":[{"id":1,"subscriptionId":"a1","eventId":1,"attempt":0,"next":"2026-11-23T00:00:00Z","done":false,"failed":false}],"suppressions":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Subscriptions) != 2 || !decoded.Subscriptions[0].Operator || decoded.Subscriptions[1].Operator || decoded.Subscriptions[1].Disabled != "" {
		t.Fatalf("old subscriptions %+v", decoded.Subscriptions)
	}
	if decoded.Deliveries[0].Reason != "" {
		t.Fatalf("old delivery %+v", decoded.Deliveries[0])
	}
	if _, err := Decode([]byte(`{"version":1,"subscriptions":[{"id":"a1","agent":"ada","inbox":"i","url":"u","secret":"s","extra":1}]}`)); err == nil {
		t.Fatal("accepted an unknown subscription field")
	}
}

func TestOldWebhookTablesGainOwnerAndStopColumns(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`ALTER TABLE webhook_subscription DROP COLUMN operator`,
		`ALTER TABLE webhook_subscription DROP COLUMN disabled`,
		`ALTER TABLE webhook_delivery DROP COLUMN fail_reason`,
		`INSERT INTO webhook_subscription (id, agent, inbox, url, secret) VALUES ('o1', 'operator', 'inbox-a', 'https://agent.example/o', 's'), ('a1', 'ada', 'inbox-a', 'https://agent.example/a', 's')`,
	} {
		if _, err := db.pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	upgraded, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	for id, operator := range map[string]bool{"o1": true, "a1": false} {
		sub, ok, err := upgraded.Subscription(ctx, id)
		if err != nil || !ok || sub.Operator != operator || sub.Disabled != "" {
			t.Fatalf("%s after upgrade %+v ok %v err %v", id, sub, ok, err)
		}
	}
	// A row stored after the upgrade keeps its own value on the next Open.
	if err := upgraded.Subscribe(ctx, Subscription{ID: "x1", Agent: "operator", Inbox: "inbox-a", URL: "https://agent.example/x", Secret: "s"}, 0); err != nil {
		t.Fatal(err)
	}
	upgraded.Close()
	reopened, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if sub, ok, err := reopened.Subscription(ctx, "x1"); err != nil || !ok || sub.Operator {
		t.Fatalf("x1 after reopen %+v ok %v err %v", sub, ok, err)
	}
	if err := reopened.Put(ctx, "inbox-a", "", "s1", []Record{{MessageID: "m", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Disable(ctx, "a1", "read grant revoked", time.Now()); err != nil {
		t.Fatal(err)
	}
}
