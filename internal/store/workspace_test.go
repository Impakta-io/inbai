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

func TestSchemaTablesHaveAWorkspaceColumn(t *testing.T) {
	var tables []string
	for _, line := range strings.Split(schema, "\n") {
		if !strings.HasPrefix(line, "CREATE TABLE") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			t.Fatalf("create line %q", line)
		}
		tables = append(tables, fields[5])
	}
	if len(tables) != len(workspaceTables) {
		t.Fatalf("tables %v workspace %v", tables, workspaceTables)
	}
	seen := map[string]bool{}
	for _, table := range tables {
		seen[table] = true
		if !strings.Contains(schema, "CREATE TABLE IF NOT EXISTS "+table+" (") {
			t.Fatalf("missing create %s", table)
		}
	}
	for _, table := range workspaceTables {
		if !seen[table] {
			t.Fatalf("workspace table %s is not created", table)
		}
	}
	if strings.Count(schema, "workspace_id text NOT NULL DEFAULT '"+WorkspaceID+"'") != len(workspaceTables) {
		t.Fatalf("schema default does not match %s", WorkspaceID)
	}
}

func TestExistingRowsGainTheWorkspace(t *testing.T) {
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
	for _, table := range workspaceTables {
		if _, err := opened.pool.Exec(ctx, `ALTER TABLE `+table+` DROP COLUMN workspace_id`); err != nil {
			opened.Close()
			t.Fatal(err)
		}
	}
	if err := opened.Remember(ctx, "inbox-a", "ada@example.com"); err != nil {
		opened.Close()
		t.Fatal(err)
	}
	opened.Close()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveGrant(ctx, "ada", "inbox-a", "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Consume(ctx, "inbox-key", "domain-key", 50, 500); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveIdempotency(ctx, "sent", "key", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveJob(ctx, JobRow{ID: "job-1", Status: "done", Total: 1, Created: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveClient(ctx, "client-1", "ada", []byte("hash"), false); err != nil {
		t.Fatal(err)
	}
	if err := db.SuspendAgent(ctx, "ada"); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendAudit(ctx, "operator", "grant", "ada"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveRetention(ctx, "server", 30); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAllowlist(ctx, "inbox-a", []string{"example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AdoptSigningKey(ctx, []byte("signing-key")); err != nil {
		t.Fatal(err)
	}
	if err := db.Suppress(ctx, "bad@example.com", "bounce"); err != nil {
		t.Fatal(err)
	}
	if err := db.Subscribe(ctx, Subscription{ID: "sub-1", Agent: "ada", Inbox: "inbox-a", URL: "https://example.com/hook", Secret: "secret"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "inbox-a", "ada@example.com", "state-1", []Record{{MessageID: "m", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Enqueue(ctx, "cursor.sweep", "singleton", []byte(`{}`), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, table := range workspaceTables {
		var count, matched int
		err := db.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE workspace_id = $1) FROM `+table, WorkspaceID).Scan(&count, &matched)
		if err != nil {
			t.Fatal(table, err)
		}
		if count == 0 || matched != count {
			t.Fatalf("%s rows %d matched %d", table, count, matched)
		}
	}
}
