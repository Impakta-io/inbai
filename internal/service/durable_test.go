// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestGrantIsVisibleToAnotherProcess(t *testing.T) {
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	if dsn == "" {
		t.Skip("INBAI_TEST_DATABASE is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`) }()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := New(&fakeMail{})
	first.SetDB(db)
	if err := first.Grant(Caller{Operator: true}, "durable-ada", "inbox-a", PermRead); err != nil {
		t.Fatal(err)
	}
	second := New(&fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "hello"}}})
	second.SetDB(db)
	got, _, err := second.List(ctx, Caller{Agent: "durable-ada"}, "inbox-a", mailbackend.Page{})
	if err != nil || len(got) != 1 {
		t.Fatalf("list %+v err %v", got, err)
	}
	if _, _, err := second.List(ctx, Caller{Agent: "durable-ada"}, "inbox-b", mailbackend.Page{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other inbox %v", err)
	}
	rows, _, err := second.Audit(ctx, Caller{Operator: true}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Action == "grant" && row.Target == "durable-ada inbox-a read" && row.Actor == "operator" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit %+v", rows)
	}
}

func TestBulkJobQueuedForAnotherReplica(t *testing.T) {
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	if dsn == "" {
		t.Skip("INBAI_TEST_DATABASE is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`) }()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := pool.Exec(ctx, `TRUNCATE job_queue, bulk_job, idempotency`); err != nil {
		t.Fatal(err)
	}

	first := New(&fakeMail{})
	first.SetDB(db)
	first.SetSyncLimit(1)

	if _, err := first.Generate(ctx, Caller{Agent: "ada"}, GenerateRequest{
		Prefix: "q", Domain: "example.com", Count: 2,
	}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent generate err %v", err)
	}

	got, err := first.Generate(ctx, Caller{Operator: true}, GenerateRequest{
		Prefix: "q", Domain: "example.com", Count: 2,
	}, "bulk-replica-key")
	if err != nil || got.JobID == "" {
		t.Fatalf("generate %+v err %v", got, err)
	}

	// Second replica claims the job from PostgreSQL and finishes it.
	secondMail := &fakeMail{}
	second := New(secondMail)
	second.SetDB(db)
	claimed, ok, err := db.ClaimJob(ctx, first.now().UTC().Add(time.Second), time.Minute)
	if err != nil || !ok || claimed.Kind != "bulk.generate" || claimed.UniqueKey != got.JobID {
		t.Fatalf("claim %+v ok %v err %v", claimed, ok, err)
	}
	var payload BulkPayload
	if err := json.Unmarshal(claimed.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if err := second.RunBulkJob(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if err := db.AckJob(ctx, claimed.ID, first.now().UTC()); err != nil {
		t.Fatal(err)
	}

	job, err := first.Job(ctx, Caller{Operator: true}, got.JobID)
	if err != nil || job.Status != "done" || job.Created != 2 || len(job.Inboxes) != 2 {
		t.Fatalf("job %+v err %v", job, err)
	}
}

func TestQueuedJobIsReadOnlyByItsAgentOrAnOperator(t *testing.T) {
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	if dsn == "" {
		t.Skip("INBAI_TEST_DATABASE is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`) }()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := pool.Exec(ctx, `TRUNCATE job_queue, bulk_job, idempotency`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM access_grant WHERE agent IN ('jobs-ada', 'jobs-bob')`); err != nil {
		t.Fatal(err)
	}
	operator := Caller{Operator: true}
	first := New(&fakeMail{})
	first.SetDB(db)
	first.SetSyncLimit(1)
	for _, agent := range []string{"jobs-ada", "jobs-bob"} {
		if err := first.Grant(operator, agent, "*", PermProvision); err != nil {
			t.Fatal(err)
		}
	}
	got, err := first.Generate(ctx, Caller{Agent: "jobs-ada"}, GenerateRequest{
		Prefix: "j", Domain: "example.com", Count: 2,
	}, "")
	if err != nil || got.JobID == "" {
		t.Fatalf("generate %+v err %v", got, err)
	}
	row, found, err := db.Job(ctx, got.JobID)
	if err != nil || !found || row.Agent != "jobs-ada" {
		t.Fatalf("stored job %+v found %v err %v", row, found, err)
	}

	// A second replica finishes the job. The stored agent is kept.
	second := New(&fakeMail{})
	second.SetDB(db)
	claimed, ok, err := db.ClaimJob(ctx, first.now().UTC().Add(time.Second), time.Minute)
	if err != nil || !ok || claimed.UniqueKey != got.JobID {
		t.Fatalf("claim %+v ok %v err %v", claimed, ok, err)
	}
	var payload BulkPayload
	if err := json.Unmarshal(claimed.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if err := second.RunBulkJob(ctx, payload); err != nil {
		t.Fatal(err)
	}

	for _, caller := range []Caller{operator, {Agent: "jobs-ada"}} {
		job, err := second.Job(ctx, caller, got.JobID)
		if err != nil || job.Status != "done" || len(job.Inboxes) != 2 {
			t.Fatalf("%+v read %+v err %v", caller, job, err)
		}
	}
	for _, caller := range []Caller{{Agent: "jobs-bob"}, {Agent: "jobs-nobody"}, {}} {
		if job, err := second.Job(ctx, caller, got.JobID); !errors.Is(err, ErrNotFound) || job.ID != "" {
			t.Fatalf("%+v read %+v err %v, want not found", caller, job, err)
		}
	}
	if err := second.Revoke(operator, "jobs-ada", "*", PermProvision); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Job(ctx, Caller{Agent: "jobs-ada"}, got.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked agent err %v, want not found", err)
	}
	if err := second.Revoke(operator, "jobs-bob", "*", PermProvision); err != nil {
		t.Fatal(err)
	}
}
