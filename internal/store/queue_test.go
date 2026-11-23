// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresQueueSkipLockedAndUnique(t *testing.T) {
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
	now := time.Now().UTC().Truncate(time.Second)

	id1, added, err := db.Enqueue(ctx, "cursor.sweep", "singleton", []byte(`{}`), now)
	if err != nil || !added || id1 == 0 {
		t.Fatalf("first enqueue id=%d added=%v err=%v", id1, added, err)
	}
	_, dup, err := db.Enqueue(ctx, "cursor.sweep", "singleton", []byte(`{}`), now)
	if err != nil || dup {
		t.Fatalf("duplicate open unique job added=%v err=%v", dup, err)
	}

	const count = 8
	for i := range count {
		if _, ok, err := db.Enqueue(ctx, "bulk.generate", "", []byte(`{"n":1}`), now); err != nil || !ok {
			t.Fatalf("enqueue %d ok=%v err=%v", i, ok, err)
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	claimed := map[int64]int{}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, ok, err := db.ClaimJob(ctx, now, time.Minute)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if !ok {
					return
				}
				mu.Lock()
				claimed[job.ID]++
				mu.Unlock()
				if err := db.AckJob(ctx, job.ID, now); err != nil {
					t.Errorf("ack %d: %v", job.ID, err)
				}
			}
		}()
	}
	wg.Wait()

	if len(claimed) != count+1 {
		t.Fatalf("claimed %d jobs, want %d", len(claimed), count+1)
	}
	for id, seen := range claimed {
		if seen != 1 {
			t.Fatalf("job %d claimed %d times", id, seen)
		}
	}

	// Once acknowledged, the same unique key can be enqueued again.
	id2, added, err := db.Enqueue(ctx, "cursor.sweep", "singleton", []byte(`{}`), now)
	if err != nil || !added || id2 == id1 {
		t.Fatalf("reenqueue id=%d added=%v err=%v", id2, added, err)
	}
	job, ok, err := db.ClaimJob(ctx, now, time.Minute)
	if err != nil || !ok || job.ID != id2 || job.Attempt != 1 {
		t.Fatalf("claim retry job=%+v ok=%v err=%v", job, ok, err)
	}
	if err := db.RetryJob(ctx, job.ID, now.Add(10*time.Second), "temporary"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.ClaimJob(ctx, now.Add(5*time.Second), time.Minute); err != nil || ok {
		t.Fatalf("claimed early ok=%v err=%v", ok, err)
	}
	job, ok, err = db.ClaimJob(ctx, now.Add(10*time.Second), time.Minute)
	if err != nil || !ok || job.ID != id2 || job.Attempt != 2 || job.LastError != "temporary" {
		t.Fatalf("second claim job=%+v ok=%v err=%v", job, ok, err)
	}
	if err := db.FailJob(ctx, job.ID, now.Add(10*time.Second), "permanent"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.ClaimJob(ctx, now.Add(time.Hour), time.Minute); err != nil || ok {
		t.Fatalf("claimed failed job ok=%v err=%v", ok, err)
	}
}
