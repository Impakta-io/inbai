// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"os"
	"testing"
)

func TestPostgresKeepsGrantsQuotaAndCredentials(t *testing.T) {
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
	defer first.Close()
	if err := reset(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveGrant(ctx, "ada", "inbox-a", "read"); err != nil {
		t.Fatal(err)
	}
	ok, err := first.Consume(ctx, "inbox", "domain", 1, 1)
	if err != nil || !ok {
		t.Fatalf("consume ok %v err %v", ok, err)
	}
	ok, err = first.Consume(ctx, "inbox", "domain", 1, 1)
	if err != nil || ok {
		t.Fatalf("second consume ok %v err %v", ok, err)
	}
	if err := first.SaveIdempotency(ctx, "sent", "k", ""); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveJob(ctx, JobRow{ID: "job", Status: "running", Total: 2}); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveClient(ctx, "client", "ada", []byte("hash"), false); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	granted, err := second.HasGrant(ctx, "ada", "inbox-a", "read")
	if err != nil || !granted {
		t.Fatalf("grant %v err %v", granted, err)
	}
	payload, found, err := second.Idempotency(ctx, "sent", "k")
	if err != nil || !found || payload != "" {
		t.Fatalf("idempotency %q found %v err %v", payload, found, err)
	}
	job, found, err := second.Job(ctx, "job")
	if err != nil || !found || job.Status != "running" || job.Total != 2 {
		t.Fatalf("job %+v found %v err %v", job, found, err)
	}
	agent, hash, operator, found, err := second.Client(ctx, "client")
	if err != nil || !found || agent != "ada" || string(hash) != "hash" || operator {
		t.Fatalf("client %s %q op %v found %v err %v", agent, hash, operator, found, err)
	}
	if err := second.SuspendAgent(ctx, "ada"); err != nil {
		t.Fatal(err)
	}
	stopped, err := second.AgentSuspended(ctx, "ada")
	if err != nil || !stopped {
		t.Fatalf("suspended %v err %v", stopped, err)
	}
}

func TestJobKeepsTheAgentThatStartedIt(t *testing.T) {
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
	if err := db.SaveJob(ctx, JobRow{ID: "agent-job", Status: "running", Total: 2, Agent: "ada"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveJob(ctx, JobRow{ID: "operator-job", Status: "running", Total: 2}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateJob(ctx, JobRow{ID: "agent-job", Status: "done", Total: 2, Created: 2, Inboxes: []byte(`[]`)}); err != nil {
		t.Fatal(err)
	}
	job, found, err := db.Job(ctx, "agent-job")
	if err != nil || !found || job.Agent != "ada" || job.Status != "done" {
		t.Fatalf("agent job %+v found %v err %v", job, found, err)
	}
	job, found, err = db.Job(ctx, "operator-job")
	if err != nil || !found || job.Agent != "" {
		t.Fatalf("operator job %+v found %v err %v", job, found, err)
	}
}

func TestOlderJobTableGainsTheAgentColumn(t *testing.T) {
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
	// Recreate the table as an older release left it, with a job already in it.
	if _, err := opened.pool.Exec(ctx, `ALTER TABLE bulk_job DROP COLUMN agent`); err != nil {
		opened.Close()
		t.Fatal(err)
	}
	if _, err := opened.pool.Exec(ctx, `INSERT INTO bulk_job (id, status, total) VALUES ('old-job', 'done', 1)`); err != nil {
		opened.Close()
		t.Fatal(err)
	}
	opened.Close()

	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	job, found, err := db.Job(ctx, "old-job")
	if err != nil || !found || job.Status != "done" || job.Agent != "" {
		t.Fatalf("old job %+v found %v err %v", job, found, err)
	}
	if err := db.SaveJob(ctx, JobRow{ID: "new-job", Status: "running", Total: 1, Agent: "ada"}); err != nil {
		t.Fatal(err)
	}
	job, found, err = db.Job(ctx, "new-job")
	if err != nil || !found || job.Agent != "ada" {
		t.Fatalf("new job %+v found %v err %v", job, found, err)
	}
}
