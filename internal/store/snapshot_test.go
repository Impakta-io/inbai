// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	original := &Memory{}
	if err := original.Remember(ctx, "inbox-a", "Ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := original.Put(ctx, "inbox-a", "", "s1", []Record{{MessageID: "m", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if err := original.Subscribe(ctx, Subscription{ID: "sub", Agent: "ada", Inbox: "inbox-a", URL: "https://agent.example/hook", Secret: "sekret"}); err != nil {
		t.Fatal(err)
	}
	if err := original.Put(ctx, "inbox-a", "", "s2", []Record{{MessageID: "m2", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	delivery, ok, err := original.Claim(ctx, time.Now())
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := original.Ack(ctx, delivery.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := original.Suppress(ctx, "bad@example.com", "delivery.dsn-perm-fail: 550"); err != nil {
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
	restored := &Memory{}
	if err := restored.Import(ctx, decoded); err != nil {
		t.Fatal(err)
	}
	reason, ok, err := restored.Suppressed(ctx, "bad@example.com")
	if err != nil || !ok || !strings.Contains(reason, "550") {
		t.Fatalf("suppressed %q ok %v err %v", reason, ok, err)
	}
	inbox, ok, err := restored.InboxByAddress(ctx, "ada@example.com")
	if err != nil || !ok || inbox != "inbox-a" {
		t.Fatalf("inbox %s ok %v err %v", inbox, ok, err)
	}
	records, err := restored.Since(ctx, "inbox-a", 0, 10)
	if err != nil || len(records) != 2 {
		t.Fatalf("events %+v err %v", records, err)
	}
	if _, ok, err = restored.Claim(ctx, time.Now().Add(time.Minute)); err != nil || ok {
		t.Fatalf("acked delivery was claimed again ok %v err %v", ok, err)
	}
	if _, err := Decode([]byte(`{"version":2}`)); err == nil {
		t.Fatal("accepted an unknown version")
	}
}

func TestPostgresBackupRoundTrip(t *testing.T) {
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
	if err := db.Remember(ctx, "inbox-a", "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "inbox-a", "", "s1", []Record{{MessageID: "m", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Subscribe(ctx, Subscription{ID: "sub", Agent: "ada", Inbox: "inbox-a", URL: "https://agent.example/hook", Secret: "sekret"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Suppress(ctx, "bad@example.com", "delivery.dsn-perm-fail: 550"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveGrant(ctx, "ada", "inbox-a", "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Consume(ctx, "inbox\ninbox-a\n2026-11-23", "domain\nexample.com\n2026-11-23", 50, 500); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveIdempotency(ctx, "sent", "ada\ninbox-a\nkey", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveJob(ctx, JobRow{ID: "job-1", Status: "done", Total: 1, Created: 1, Inboxes: []byte(`[{"id":"a","address":"a@example.com"}]`)}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveClient(ctx, "client-1", "ada", []byte("hash"), false); err != nil {
		t.Fatal(err)
	}
	if err := db.SuspendAgent(ctx, "ada"); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendAudit(ctx, "operator", "grant", "ada inbox-a read"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveRetention(ctx, "server", 14); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAllowlist(ctx, "inbox-a", []string{"example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AdoptSigningKey(ctx, []byte("signing-key")); err != nil {
		t.Fatal(err)
	}
	snap, err := db.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Encode(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Import(ctx, emptySnapshot()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.Suppressed(ctx, "bad@example.com"); err != nil || ok {
		t.Fatalf("wipe left a suppression ok %v err %v", ok, err)
	}
	if granted, err := db.HasGrant(ctx, "ada", "inbox-a", "read"); err != nil || granted {
		t.Fatalf("wipe left a grant %v err %v", granted, err)
	}
	decoded, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Import(ctx, decoded); err != nil {
		t.Fatal(err)
	}
	reason, ok, err := db.Suppressed(ctx, "bad@example.com")
	if err != nil || !ok || !strings.Contains(reason, "550") {
		t.Fatalf("restored %q ok %v err %v", reason, ok, err)
	}
	records, err := db.Since(ctx, "inbox-a", 0, 10)
	if err != nil || len(records) != 1 || records[0].MessageID != "m" {
		t.Fatalf("events %+v err %v", records, err)
	}
	if err := db.Put(ctx, "inbox-a", "", "s2", []Record{{MessageID: "m2", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	again, err := db.Since(ctx, "inbox-a", records[0].ID, 10)
	if err != nil || len(again) != 1 || again[0].MessageID != "m2" {
		t.Fatalf("identity after restore %+v err %v", again, err)
	}
	granted, err := db.HasGrant(ctx, "ada", "inbox-a", "read")
	if err != nil || !granted {
		t.Fatalf("grant restored %v err %v", granted, err)
	}
	stopped, err := db.AgentSuspended(ctx, "ada")
	if err != nil || !stopped {
		t.Fatalf("suspension restored %v err %v", stopped, err)
	}
	_, found, err := db.Idempotency(ctx, "sent", "ada\ninbox-a\nkey")
	if err != nil || !found {
		t.Fatalf("idempotency restored found %v err %v", found, err)
	}
	job, found, err := db.Job(ctx, "job-1")
	if err != nil || !found || job.Status != "done" || job.Created != 1 {
		t.Fatalf("job restored %+v found %v err %v", job, found, err)
	}
	audits, err := db.AuditSince(ctx, 0, 10)
	if err != nil || len(audits) != 1 || audits[0].Action != "grant" || audits[0].Target != "ada inbox-a read" {
		t.Fatalf("audit %+v err %v", audits, err)
	}
	days, ok, err := db.Retention(ctx, "server")
	if err != nil || !ok || days != 14 {
		t.Fatalf("retention %d ok %v err %v", days, ok, err)
	}
	senders, ok, err := db.Allowlist(ctx, "inbox-a")
	if err != nil || !ok || len(senders) != 1 || senders[0] != "example.com" {
		t.Fatalf("allowlist %+v ok %v err %v", senders, ok, err)
	}
	key, err := db.AdoptSigningKey(ctx, nil)
	if err != nil || !bytes.Equal(key, []byte("signing-key")) {
		t.Fatalf("signing key %q err %v", key, err)
	}
}

func TestOldSnapshotOmitsTheNewTables(t *testing.T) {
	decoded, err := Decode([]byte(`{"version":1,"cursors":[],"events":[],"subscriptions":[],"deliveries":[],"suppressions":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Grants != nil || decoded.Clients != nil || decoded.Jobs != nil || decoded.Audits != nil || decoded.Retention != nil || decoded.Allowlists != nil || decoded.SigningKey != nil {
		t.Fatalf("old file gained tables %+v", decoded)
	}
}
