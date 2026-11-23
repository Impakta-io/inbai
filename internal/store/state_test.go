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
