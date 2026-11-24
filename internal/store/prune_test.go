// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"os"
	"testing"
	"time"
)

const day = 24 * time.Hour

var testRetention = Retention{DoneJobs: day, FailedJobs: 7 * day, Deliveries: 7 * day, Events: 30 * day}

type prunableLog interface {
	Log
	Pruner
	Export(context.Context) (Snapshot, error)
	Import(context.Context, Snapshot) error
}

func TestMemoryPrunesDeliveriesAndEvents(t *testing.T) {
	checkPruneEvents(t, &Memory{}, &Memory{})
}

func TestPostgresPrunesDeliveriesAndEvents(t *testing.T) {
	db := openTestDB(t)
	checkPruneEvents(t, db, db)
}

// checkPruneEvents logs e1 before the webhook exists, then e2 (delivered), e3 (failed),
// and e4 (still pending), and prunes at later and later times.
func checkPruneEvents(t *testing.T, log, restore prunableLog) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	if err := log.Put(ctx, "inbox-a", "", "s1", []Record{{MessageID: "e1", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if err := log.Subscribe(ctx, Subscription{ID: "sub", Agent: "ada", Inbox: "inbox-a", URL: "https://agent.example/", Secret: "s"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := log.Put(ctx, "inbox-a", "", "s2", []Record{{MessageID: "e2", Kind: "created"}, {MessageID: "e3", Kind: "created"}, {MessageID: "e4", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	claim := func() Delivery {
		t.Helper()
		delivery, ok, err := log.Claim(ctx, now.Add(time.Second))
		if err != nil || !ok {
			t.Fatalf("claim ok %v err %v", ok, err)
		}
		return delivery
	}
	if err := log.Ack(ctx, claim().ID, now); err != nil {
		t.Fatal(err)
	}
	if err := log.Fail(ctx, claim().ID, now); err != nil {
		t.Fatal(err)
	}
	pending := claim()
	if err := log.Retry(ctx, pending.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ids := func() []string {
		t.Helper()
		records, err := log.Since(ctx, "inbox-a", 0, 50)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, record := range records {
			out = append(out, record.MessageID)
		}
		return out
	}

	if got, err := log.Prune(ctx, now.Add(6*day), testRetention); err != nil || got != (Pruned{}) {
		t.Fatalf("nothing is old enough yet, pruned %+v err %v", got, err)
	}
	if got, err := log.Prune(ctx, now.Add(8*day), testRetention); err != nil || got.Deliveries != 2 || got.Events != 0 {
		t.Fatalf("finished deliveries after 8 days, pruned %+v err %v", got, err)
	}
	got, err := log.Prune(ctx, now.Add(31*day), testRetention)
	if err != nil || got.Events != 3 || got.Deliveries != 0 {
		t.Fatalf("old events after 31 days, pruned %+v err %v", got, err)
	}
	// A consumer whose cursor is older than every event left reads from the oldest kept one.
	if left := ids(); len(left) != 1 || left[0] != "e4" {
		t.Fatalf("kept events %v", left)
	}
	if head, err := log.Head(ctx, "inbox-a"); err != nil || head != pending.Event.ID {
		t.Fatalf("head %d err %v", head, err)
	}
	if again, ok, err := log.Claim(ctx, now.Add(2*time.Hour)); err != nil || !ok || again.ID != pending.ID || again.Event.MessageID != "e4" {
		t.Fatalf("pending delivery after pruning %+v ok %v err %v", again, ok, err)
	}
	if err := log.Ack(ctx, pending.ID, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := log.Prune(ctx, now.Add(40*day), testRetention); err != nil || got.Deliveries != 1 || got.Events != 1 {
		t.Fatalf("last delivery and event, pruned %+v err %v", got, err)
	}
	if left := ids(); len(left) != 0 {
		t.Fatalf("kept events %v", left)
	}

	// A backup of the empty log still carries the last id, so a restore never reuses one.
	snap, err := log.Export(ctx)
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
	if decoded.EventSequence < pending.Event.ID {
		t.Fatalf("event sequence %d, last id %d", decoded.EventSequence, pending.Event.ID)
	}
	if err := restore.Import(ctx, decoded); err != nil {
		t.Fatal(err)
	}
	if err := restore.Put(ctx, "inbox-a", "", "s3", []Record{{MessageID: "e5", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	records, err := restore.Since(ctx, "inbox-a", pending.Event.ID, 50)
	if err != nil || len(records) != 1 || records[0].MessageID != "e5" {
		t.Fatalf("event after restore %+v err %v", records, err)
	}
}

type prunableQueue interface {
	Queue
	Pruner
}

func TestMemoryQueuePrunesFinishedJobs(t *testing.T) {
	checkPruneJobs(t, &MemoryQueue{})
}

func TestPostgresQueuePrunesFinishedJobs(t *testing.T) {
	checkPruneJobs(t, openTestDB(t))
}

func checkPruneJobs(t *testing.T, queue prunableQueue) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, key := range []string{"done", "failed", "open"} {
		if _, ok, err := queue.Enqueue(ctx, "test", key, nil, now.Add(-time.Second)); err != nil || !ok {
			t.Fatalf("enqueue %s ok %v err %v", key, ok, err)
		}
	}
	for _, finish := range []func(int64) error{
		func(id int64) error { return queue.AckJob(ctx, id, now) },
		func(id int64) error { return queue.FailJob(ctx, id, now, "broken") },
	} {
		job, ok, err := queue.ClaimJob(ctx, now, time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim ok %v err %v", ok, err)
		}
		if err := finish(job.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := queue.Prune(ctx, now.Add(12*time.Hour), testRetention); err != nil || got.Jobs != 0 {
		t.Fatalf("after 12 hours pruned %+v err %v", got, err)
	}
	if got, err := queue.Prune(ctx, now.Add(2*day), testRetention); err != nil || got.Jobs != 1 {
		t.Fatalf("after 2 days pruned %+v err %v", got, err)
	}
	if got, err := queue.Prune(ctx, now.Add(8*day), testRetention); err != nil || got.Jobs != 1 {
		t.Fatalf("after 8 days pruned %+v err %v", got, err)
	}
	open, ok, err := queue.ClaimJob(ctx, now.Add(9*day), time.Minute)
	if err != nil || !ok || open.UniqueKey != "open" {
		t.Fatalf("open job %+v ok %v err %v", open, ok, err)
	}
	if _, ok, err := queue.ClaimJob(ctx, now.Add(10*day), time.Minute); err != nil || !ok {
		t.Fatalf("only the open job is left, ok %v err %v", ok, err)
	}
}

func TestPostgresPrunesSpentSendCounters(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	keys := map[string]bool{
		"inbox\ninbox-a\n" + old.Format(time.DateOnly):                           false,
		"rcpt\nexample.com\n" + now.Add(-2*time.Hour).Format("2006-01-02T15:04"): false,
		"inbox\ninbox-a\n" + now.Format(time.DateOnly):                           true,
		"rcpt\nexample.com\n" + now.Format("2006-01-02T15:04"):                   true,
		"custom\nno-window": true,
	}
	for key := range keys {
		if ok, err := db.ConsumeBuckets(ctx, []BucketLimit{{Key: key, Limit: 10}}); err != nil || !ok {
			t.Fatalf("consume %q ok %v err %v", key, ok, err)
		}
	}
	got, err := db.Prune(ctx, now, Retention{})
	if err != nil || got.Quotas != 2 {
		t.Fatalf("pruned %+v err %v", got, err)
	}
	snap, err := db.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	for _, row := range snap.Quotas {
		left[row.Bucket] = true
	}
	for key, kept := range keys {
		if left[key] != kept {
			t.Fatalf("%q kept %v, want %v", key, left[key], kept)
		}
	}
}

func TestOldSnapshotEventsRestoreWithoutATime(t *testing.T) {
	const old = `{"version":1,"cursors":[],"events":[{"id":7,"inbox":"inbox-a","messageId":"m","kind":"created"}],"subscriptions":[],"deliveries":[],"suppressions":[]}`
	decoded, err := Decode([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.EventSequence != 0 || !decoded.Events[0].At.IsZero() {
		t.Fatalf("old file gained fields %+v", decoded)
	}
	ctx := context.Background()
	restored := &Memory{}
	if err := restored.Import(ctx, decoded); err != nil {
		t.Fatal(err)
	}
	// The event counts from the restore, so pruning a day later keeps it.
	if got, err := restored.Prune(ctx, time.Now().Add(day), testRetention); err != nil || got.Events != 0 {
		t.Fatalf("pruned %+v err %v", got, err)
	}
	if err := restored.Put(ctx, "inbox-a", "", "s", []Record{{MessageID: "n", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if head, err := restored.Head(ctx, "inbox-a"); err != nil || head != 8 {
		t.Fatalf("head %d err %v", head, err)
	}
}

func TestPostgresRestoresEventTimes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	snap := emptySnapshot()
	snap.Events = []EventRow{
		{Record: Record{ID: 3, Inbox: "inbox-a", MessageID: "dated", Kind: "created"}, At: at},
		{Record: Record{ID: 4, Inbox: "inbox-a", MessageID: "undated", Kind: "created"}},
	}
	snap.EventSequence = 9
	if err := db.Import(ctx, snap); err != nil {
		t.Fatal(err)
	}
	back, err := db.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Events) != 2 || !back.Events[0].At.Equal(at) || time.Since(back.Events[1].At) > time.Minute {
		t.Fatalf("event times %+v", back.Events)
	}
	if back.EventSequence != 9 {
		t.Fatalf("event sequence %d", back.EventSequence)
	}
	if err := db.Put(ctx, "inbox-a", "", "s", []Record{{MessageID: "n", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if head, err := db.Head(ctx, "inbox-a"); err != nil || head != 10 {
		t.Fatalf("head %d err %v", head, err)
	}
	got, err := db.Prune(ctx, at.Add(31*day), testRetention)
	if err != nil || got.Events != 1 {
		t.Fatalf("pruned %+v err %v", got, err)
	}
}

func TestOldTablesGainEventTimeAndSendingColumns(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`ALTER TABLE mail_event DROP COLUMN at`,
		`ALTER TABLE webhook_subscription DROP COLUMN sending_delivery`,
		`ALTER TABLE webhook_subscription DROP COLUMN sending_until`,
		`INSERT INTO webhook_subscription (id, agent, inbox, url, secret) VALUES ('a1', 'ada', 'inbox-a', 'https://agent.example/a', 's')`,
		`INSERT INTO mail_event (inbox, message_id, kind) VALUES ('inbox-a', 'old', 'created')`,
		`INSERT INTO webhook_delivery (subscription_id, event_id) SELECT 'a1', id FROM mail_event`,
	} {
		if _, err := db.pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	upgraded, err := Open(ctx, os.Getenv("INBAI_TEST_DATABASE"))
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	now := time.Now()
	claimed, ok, err := upgraded.Claim(ctx, now.Add(time.Second))
	if err != nil || !ok || claimed.Event.MessageID != "old" {
		t.Fatalf("claim after upgrade %+v ok %v err %v", claimed, ok, err)
	}
	if err := upgraded.Ack(ctx, claimed.ID, now); err != nil {
		t.Fatal(err)
	}
	// The old event counts from the upgrade: kept 29 days on, pruned after 30.
	if got, err := upgraded.Prune(ctx, now.Add(29*day), testRetention); err != nil || got.Events != 0 || got.Deliveries != 1 {
		t.Fatalf("29 days after the upgrade pruned %+v err %v", got, err)
	}
	if got, err := upgraded.Prune(ctx, now.Add(31*day), testRetention); err != nil || got.Events != 1 {
		t.Fatalf("31 days after the upgrade pruned %+v err %v", got, err)
	}
}
