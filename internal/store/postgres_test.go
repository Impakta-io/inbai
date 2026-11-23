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

func reset(ctx context.Context, db *Postgres) error {
	_, err := db.pool.Exec(ctx, `TRUNCATE webhook_delivery, webhook_subscription, mail_event, inbox_cursor, suppression, access_grant, quota_counter, idempotency, bulk_job, agent_credential, agent_suspension, audit_event, retention_policy, signing_key`)
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
