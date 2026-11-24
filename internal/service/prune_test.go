// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPruneMemoryCounters(t *testing.T) {
	svc := New(&fakeMail{})
	now := time.Date(2026, 11, 24, 12, 30, 0, 0, time.UTC)
	svc.sent["inbox\na\n2026-11-24"] = 1
	svc.sent["inbox\na\n2026-11-23"] = 1
	svc.sent["rcpt\nx.com\n2026-11-24T12:30"] = 1
	svc.sent["rcpt\nx.com\n2026-11-24T11:31"] = 1 // inside the grace hour
	svc.sent["rcpt\nx.com\n2026-11-24T11:29"] = 1
	svc.sent["odd"] = 1
	counters, _ := svc.PruneMemory(now)
	if counters != 2 {
		t.Fatalf("removed %d counters, want 2", counters)
	}
	for _, gone := range []string{"inbox\na\n2026-11-23", "rcpt\nx.com\n2026-11-24T11:29"} {
		if _, ok := svc.sent[gone]; ok {
			t.Errorf("%q kept", gone)
		}
	}
	if len(svc.sent) != 4 {
		t.Errorf("kept %d counters, want 4", len(svc.sent))
	}
}

func TestPruneMemoryIdempotency(t *testing.T) {
	svc := New(&fakeMail{})
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	svc.idem["sent\nold-done"] = idemEntry{done: true, at: now.Add(-idempotencyMemoryRetention - time.Minute)}
	svc.idem["sent\nyoung-done"] = idemEntry{done: true, at: now.Add(-time.Hour)}
	svc.idem["sent\nyoung-busy"] = idemEntry{at: now.Add(-idempotencyTakeover + time.Minute)}
	svc.idem["sent\nold-busy"] = idemEntry{at: now.Add(-idempotencyMemoryRetention - time.Minute)}
	_, keys := svc.PruneMemory(now)
	if keys != 2 {
		t.Fatalf("removed %d keys, want 2", keys)
	}
	if _, ok := svc.idem["sent\nyoung-done"]; !ok {
		t.Error("young done key removed")
	}
	if _, ok := svc.idem["sent\nyoung-busy"]; !ok {
		t.Error("in-progress key removed")
	}
}

func TestQuotaHoldsAfterPruneWithinWindow(t *testing.T) {
	ctx := context.Background()
	svc := New(&fakeMail{})
	svc.SetQuota(2, 100)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	for range 2 {
		if err := svc.Deliver(ctx, operator, outbound("hi"), ""); err != nil {
			t.Fatal(err)
		}
		now = now.Add(2 * time.Minute)
	}
	svc.PruneMemory(now)
	if err := svc.Deliver(ctx, operator, outbound("hi"), ""); !errors.Is(err, ErrQuota) {
		t.Fatalf("err = %v, want ErrQuota after a prune in the same day", err)
	}
	now = now.Add(24 * time.Hour)
	if c, _ := svc.PruneMemory(now); c == 0 {
		t.Fatal("old counters kept")
	}
	if err := svc.Deliver(ctx, operator, outbound("hi"), ""); err != nil {
		t.Fatalf("next day err = %v", err)
	}
}
