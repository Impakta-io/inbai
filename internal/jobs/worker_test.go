// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/store"
)

func TestWorkerRetriesThenAcks(t *testing.T) {
	q := &store.MemoryQueue{}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	worker := New(q)
	worker.now = func() time.Time { return now }
	worker.baseBackoff = 10 * time.Second

	calls := 0
	worker.Handle("test.job", func(_ context.Context, payload json.RawMessage) error {
		calls++
		if string(payload) != `{"x":1}` {
			t.Errorf("payload %s", payload)
		}
		if calls == 1 {
			return errors.New("try again")
		}
		return nil
	})

	ctx := context.Background()
	if _, ok, err := q.Enqueue(ctx, "test.job", "k", []byte(`{"x":1}`), now); err != nil || !ok {
		t.Fatalf("enqueue ok=%v err=%v", ok, err)
	}
	if ok, err := worker.RunOnce(ctx); err != nil || !ok {
		t.Fatalf("first run ok=%v err=%v", ok, err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	if ok, err := worker.RunOnce(ctx); err != nil || ok {
		t.Fatalf("ran before backoff ok=%v err=%v", ok, err)
	}
	now = now.Add(10 * time.Second)
	if ok, err := worker.RunOnce(ctx); err != nil || !ok {
		t.Fatalf("second run ok=%v err=%v", ok, err)
	}
	jobs := q.Jobs()
	if len(jobs) != 1 || !jobs[0].Done || jobs[0].Failed || jobs[0].Attempt != 2 {
		t.Fatalf("jobs %+v", jobs)
	}
}

func TestWorkerFailsPermanentAndUnknownImmediately(t *testing.T) {
	q := &store.MemoryQueue{}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	worker := New(q)
	worker.now = func() time.Time { return now }

	calls := 0
	worker.Handle("perm.job", func(context.Context, json.RawMessage) error {
		calls++
		return Permanent(errors.New("bad input"))
	})

	ctx := context.Background()
	if _, _, err := q.Enqueue(ctx, "perm.job", "", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.Enqueue(ctx, "unknown.job", "", nil, now); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("run ok=%v err=%v", ok, err)
		}
	}
	if ok, err := worker.RunOnce(ctx); err != nil || ok {
		t.Fatalf("third run ok=%v err=%v", ok, err)
	}
	if calls != 1 {
		t.Fatalf("permanent job retried, calls=%d", calls)
	}
	for _, job := range q.Jobs() {
		if !job.Failed || job.Done {
			t.Fatalf("expected failed job %+v", job)
		}
	}
}

func TestTwoWorkersSplitQueuedJobs(t *testing.T) {
	q := &store.MemoryQueue{}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	first := New(q)
	second := New(q)
	first.now = func() time.Time { return now }
	second.now = func() time.Time { return now }

	var seenFirst, seenSecond int
	first.Handle(KindCursorSweep, func(context.Context, json.RawMessage) error {
		seenFirst++
		return nil
	})
	second.Handle(KindCursorSweep, func(context.Context, json.RawMessage) error {
		seenSecond++
		return nil
	})

	ctx := context.Background()
	if _, ok, err := q.Enqueue(ctx, KindCursorSweep, "singleton", nil, now); err != nil || !ok {
		t.Fatalf("first enqueue ok=%v err=%v", ok, err)
	}
	if _, ok, err := q.Enqueue(ctx, KindCursorSweep, "singleton", nil, now); err != nil || ok {
		t.Fatalf("duplicate singleton ok=%v err=%v", ok, err)
	}
	if ok, err := first.RunOnce(ctx); err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	if ok, err := second.RunOnce(ctx); err != nil || ok {
		t.Fatalf("second claimed same singleton ok=%v err=%v", ok, err)
	}
	if seenFirst != 1 || seenSecond != 0 {
		t.Fatalf("first=%d second=%d", seenFirst, seenSecond)
	}
}
