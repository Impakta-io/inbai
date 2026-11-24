// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresKeepsEventsAfterReconnect(t *testing.T) {
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	if dsn == "" {
		t.Skip("INBAI_TEST_DATABASE is not set")
	}
	ctx := context.Background()
	lockTestDB(t, dsn)
	first, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := reset(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := first.Remember(ctx, "inbox-a", "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := first.Subscribe(ctx, Subscription{ID: "sub", Agent: "ada", Inbox: "inbox-a", URL: "http://127.0.0.1/hook", Secret: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Put(ctx, "inbox-a", "", "s1", []Record{{MessageID: "m", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	records, err := second.Since(ctx, "inbox-a", 0, 10)
	if err != nil || len(records) != 1 || records[0].MessageID != "m" {
		t.Fatalf("records %+v err %v", records, err)
	}
	if head, err := second.Head(ctx, "inbox-a"); err != nil || head != records[0].ID {
		t.Fatalf("head %d err %v", head, err)
	}
	if head, err := second.Head(ctx, "inbox-b"); err != nil || head != 0 {
		t.Fatalf("empty head %d err %v", head, err)
	}
	inbox, ok, err := second.InboxByAddress(ctx, "ada@example.com")
	if err != nil || !ok || inbox != "inbox-a" {
		t.Fatalf("inbox %s ok %v err %v", inbox, ok, err)
	}
	delivery, ok, err := second.Claim(ctx, time.Now())
	if err != nil || !ok || delivery.Event.MessageID != "m" || delivery.Secret != "s" {
		t.Fatalf("delivery %+v ok %v err %v", delivery, ok, err)
	}
	if _, ok, err = second.Claim(ctx, time.Now()); err != nil || ok {
		t.Fatalf("second claim ok %v err %v", ok, err)
	}
	if err := second.Suppress(ctx, "Bad@example.com", "delivery.dsn-perm-fail: 550 user unknown"); err != nil {
		t.Fatal(err)
	}
	second.Close()
	third, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	reason, ok, err := third.Suppressed(ctx, "bad@example.com")
	if err != nil || !ok || !strings.Contains(reason, "550") {
		t.Fatalf("suppressed %q ok %v err %v", reason, ok, err)
	}
}

func TestMemoryForgetDropsTheCursor(t *testing.T) {
	checkForget(t, &Memory{})
}

func TestPostgresForgetDropsTheCursor(t *testing.T) {
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
	checkForget(t, db)
}

// checkForget removes one of two inboxes and checks the other is untouched.
func checkForget(t *testing.T, log Log) {
	t.Helper()
	ctx := context.Background()
	if err := log.Put(ctx, "inbox-a", "ada@example.com", "s1", []Record{{MessageID: "m", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if err := log.Put(ctx, "inbox-b", "bob@example.com", "s2", nil); err != nil {
		t.Fatal(err)
	}
	if err := log.Forget(ctx, "inbox-a"); err != nil {
		t.Fatal(err)
	}
	if err := log.Forget(ctx, "inbox-missing"); err != nil {
		t.Fatalf("forget missing %v", err)
	}
	inboxes, err := log.Inboxes(ctx)
	if err != nil || len(inboxes) != 1 || inboxes[0] != "inbox-b" {
		t.Fatalf("inboxes %v err %v", inboxes, err)
	}
	if _, ok, err := log.State(ctx, "inbox-a"); err != nil || ok {
		t.Fatalf("state ok %v err %v", ok, err)
	}
	if _, ok, err := log.InboxByAddress(ctx, "ada@example.com"); err != nil || ok {
		t.Fatalf("address ok %v err %v", ok, err)
	}
	if state, ok, err := log.State(ctx, "inbox-b"); err != nil || !ok || state != "s2" {
		t.Fatalf("other state %q ok %v err %v", state, ok, err)
	}
	if inbox, ok, err := log.InboxByAddress(ctx, "bob@example.com"); err != nil || !ok || inbox != "inbox-b" {
		t.Fatalf("other address %q ok %v err %v", inbox, ok, err)
	}
	records, err := log.Since(ctx, "inbox-a", 0, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("logged events should stay: %+v err %v", records, err)
	}
}

func reset(ctx context.Context, db *Postgres) error {
	_, err := db.pool.Exec(ctx, `TRUNCATE webhook_delivery, webhook_subscription, mail_event, inbox_cursor, suppression, access_grant, quota_counter, idempotency, bulk_job, agent_credential, agent_suspension, audit_event, retention_policy, sender_allowlist, signing_key, job_queue`)
	return err
}

func lockTestDB(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
		_ = conn.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`)
		_ = conn.Close(context.Background())
	})
}
