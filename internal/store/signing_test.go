// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestAdoptSigningKeyIsShared(t *testing.T) {
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
	first, err := db.AdoptSigningKey(ctx, nil)
	if err != nil || len(first) != 32 {
		t.Fatalf("key %d err %v", len(first), err)
	}
	second, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	again, err := second.AdoptSigningKey(ctx, nil)
	if err != nil || !bytes.Equal(again, first) {
		t.Fatal("second process did not see the stored key")
	}
	if _, err := second.AdoptSigningKey(ctx, []byte("other-key")); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v", err)
	}
	same, err := db.AdoptSigningKey(ctx, first)
	if err != nil || !bytes.Equal(same, first) {
		t.Fatalf("explicit key err %v", err)
	}
}
