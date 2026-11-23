// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"os"
	"testing"

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
