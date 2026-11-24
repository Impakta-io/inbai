// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// oldIdempotencyBackup is a file written before keys were reserved and bound to a request.
const oldIdempotencyBackup = `{"version":1,"cursors":[],"events":[],"subscriptions":[],"deliveries":[],"suppressions":[],` +
	`"grants":[],"quotas":[],"jobs":[],"clients":[],"suspensions":[],` +
	`"idempotency":[{"kind":"sent","key":"ada\ninbox-a\nold","payload":""}]}`

func TestSnapshotKeepsTheIdempotencyState(t *testing.T) {
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	snap := emptySnapshot()
	snap.Idempotency = []IdempotencyRow{
		{Kind: "sent", Key: "k1", State: IdempotencyDone, RequestHash: "h1"},
		{Kind: "reply", Key: "k2", State: IdempotencyInProgress, RequestHash: "h2", ReservedAt: at},
	}
	raw, err := Encode(snap)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Idempotency) != 2 || decoded.Idempotency[1].State != IdempotencyInProgress ||
		decoded.Idempotency[1].RequestHash != "h2" || !decoded.Idempotency[1].ReservedAt.Equal(at) {
		t.Fatalf("idempotency %+v", decoded.Idempotency)
	}
	old, err := Decode([]byte(oldIdempotencyBackup))
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Idempotency) != 1 || old.Idempotency[0].State != "" || old.Idempotency[0].RequestHash != "" {
		t.Fatalf("old idempotency %+v", old.Idempotency)
	}
	bad := emptySnapshot()
	bad.Idempotency = []IdempotencyRow{{Kind: "sent", Key: "k", State: "maybe"}}
	if _, err := Encode(bad); err == nil || !strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("unknown state err %v", err)
	}
}

func TestPostgresReservesIdempotencyKeys(t *testing.T) {
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
	defer db.Close()
	if err := reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 24, 12, 0, 0, 123456000, time.UTC)
	stale := at.Add(-10 * time.Minute)
	reserved, _, err := db.ReserveIdempotency(ctx, "sent", "k", "h", at, stale)
	if err != nil || !reserved {
		t.Fatalf("first reserve %v err %v", reserved, err)
	}
	reserved, found, err := db.ReserveIdempotency(ctx, "sent", "k", "h", at, stale)
	if err != nil || reserved || found.State != IdempotencyInProgress || found.RequestHash != "h" {
		t.Fatalf("second reserve %v %+v err %v", reserved, found, err)
	}
	if reserved, _, err := db.ReserveIdempotency(ctx, "reply", "k", "h", at, stale); err != nil || !reserved {
		t.Fatalf("other namespace %v err %v", reserved, err)
	}
	later := at.Add(11 * time.Minute)
	if reserved, _, err := db.ReserveIdempotency(ctx, "sent", "k", "other", later, later.Add(-10*time.Minute)); err != nil || reserved {
		t.Fatalf("stale key taken over by a different request %v err %v", reserved, err)
	}
	if reserved, _, err := db.ReserveIdempotency(ctx, "sent", "k", "h", later, later.Add(-10*time.Minute)); err != nil || !reserved {
		t.Fatalf("stale takeover %v err %v", reserved, err)
	}
	// The first holder no longer owns the key, so its completion and release change nothing.
	if err := db.CompleteIdempotency(ctx, "sent", "k", at, "old"); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseIdempotency(ctx, "sent", "k", at); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteIdempotency(ctx, "sent", "k", later, "id-1"); err != nil {
		t.Fatal(err)
	}
	_, found, err = db.ReserveIdempotency(ctx, "sent", "k", "h", later, later)
	if err != nil || found.State != IdempotencyDone || found.Payload != "id-1" {
		t.Fatalf("completed %+v err %v", found, err)
	}
	if err := db.ReleaseIdempotency(ctx, "reply", "k", at); err != nil {
		t.Fatal(err)
	}
	if reserved, _, err := db.ReserveIdempotency(ctx, "reply", "k", "h2", at, stale); err != nil || !reserved {
		t.Fatalf("released key reserve %v err %v", reserved, err)
	}

	snap, err := db.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Import(ctx, emptySnapshot()); err != nil {
		t.Fatal(err)
	}
	if err := db.Import(ctx, snap); err != nil {
		t.Fatal(err)
	}
	_, found, err = db.ReserveIdempotency(ctx, "reply", "k", "h2", at, stale)
	if err != nil || found.State != IdempotencyInProgress || found.RequestHash != "h2" {
		t.Fatalf("restored reservation %+v err %v", found, err)
	}
	if reserved, _, err := db.ReserveIdempotency(ctx, "reply", "k", "h2", later, later.Add(-10*time.Minute)); err != nil || !reserved {
		t.Fatalf("restored reservation kept its age: takeover %v err %v", reserved, err)
	}

	old, err := Decode([]byte(oldIdempotencyBackup))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Import(ctx, old); err != nil {
		t.Fatal(err)
	}
	_, found, err = db.ReserveIdempotency(ctx, "sent", "ada\ninbox-a\nold", "h", at, stale)
	if err != nil || found.State != IdempotencyDone || found.RequestHash != "" {
		t.Fatalf("old backup row %+v err %v", found, err)
	}
}

func TestPostgresAddsIdempotencyColumnsToAnOldTable(t *testing.T) {
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	if dsn == "" {
		t.Skip("INBAI_TEST_DATABASE is not set")
	}
	ctx := context.Background()
	lockTestDB(t, dsn)
	opened, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := reset(ctx, opened); err != nil {
		opened.Close()
		t.Fatal(err)
	}
	for _, column := range []string{"state", "request_hash", "reserved_at"} {
		if _, err := opened.pool.Exec(ctx, `ALTER TABLE idempotency DROP COLUMN `+column); err != nil {
			opened.Close()
			t.Fatal(err)
		}
	}
	if _, err := opened.pool.Exec(ctx, `INSERT INTO idempotency (kind, key, payload) VALUES ('created', 'k', 'id-1')`); err != nil {
		opened.Close()
		t.Fatal(err)
	}
	opened.Close()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	reserved, found, err := db.ReserveIdempotency(ctx, "created", "k", "h", now, now.Add(-10*time.Minute))
	if err != nil || reserved || found.State != IdempotencyDone || found.Payload != "id-1" || found.RequestHash != "" {
		t.Fatalf("old row after upgrade reserved %v %+v err %v", reserved, found, err)
	}
	payload, ok, err := db.Idempotency(ctx, "created", "k")
	if err != nil || !ok || payload != "id-1" {
		t.Fatalf("payload %q ok %v err %v", payload, ok, err)
	}
}
