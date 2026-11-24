// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestMemoryClaimsOneDeliveryPerSubscription(t *testing.T) {
	checkOneInFlight(t, &Memory{})
}

func TestPostgresClaimsOneDeliveryPerSubscription(t *testing.T) {
	checkOneInFlight(t, openTestDB(t))
}

// checkOneInFlight holds a subscription's delivery in flight, so its next one waits while
// another subscription's goes, until the first finishes or its lease ends.
func checkOneInFlight(t *testing.T, log Log) {
	t.Helper()
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if err := log.Subscribe(ctx, Subscription{ID: id, Agent: "ada", Inbox: "inbox-" + id, URL: "https://agent.example/" + id, Secret: "s"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, inbox := range []string{"inbox-a", "inbox-b"} {
		if err := log.Put(ctx, inbox, "", "s1", []Record{{MessageID: "m1", Kind: "created"}, {MessageID: "m2", Kind: "created"}}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Add(time.Second)
	first, ok, err := log.Claim(ctx, now)
	if err != nil || !ok || first.SubscriptionID != "a" || first.Event.MessageID != "m1" {
		t.Fatalf("first %+v ok %v err %v", first, ok, err)
	}
	other, ok, err := log.Claim(ctx, now)
	if err != nil || !ok || other.SubscriptionID != "b" || other.Event.MessageID != "m1" {
		t.Fatalf("a's second delivery must wait; got %+v ok %v err %v", other, ok, err)
	}
	if got, ok, err := log.Claim(ctx, now); err != nil || ok {
		t.Fatalf("both webhooks are busy, claimed %+v err %v", got, err)
	}
	if err := log.Ack(ctx, first.ID, now); err != nil {
		t.Fatal(err)
	}
	next, ok, err := log.Claim(ctx, now)
	if err != nil || !ok || next.SubscriptionID != "a" || next.Event.MessageID != "m2" {
		t.Fatalf("after the ack %+v ok %v err %v", next, ok, err)
	}
	if err := log.Ack(ctx, next.ID, now); err != nil {
		t.Fatal(err)
	}
	// b's delivery is never acknowledged, as when a process stops mid-post. Its lease
	// ends and the same delivery is claimed again before b's later one.
	again, ok, err := log.Claim(ctx, now.Add(31*time.Second))
	if err != nil || !ok || again.ID != other.ID || again.Attempt != 2 {
		t.Fatalf("after the lease %+v ok %v err %v", again, ok, err)
	}
}

func TestMemoryLateOutcomes(t *testing.T) {
	checkLateOutcomes(t, &Memory{})
}

func TestPostgresLateOutcomes(t *testing.T) {
	checkLateOutcomes(t, openTestDB(t))
}

// checkLateOutcomes reports outcomes from a sender whose lease ended. A delivery the
// receiver took is recorded as delivered, a finished delivery is not changed again, and
// another delivery's lease is left alone.
func checkLateOutcomes(t *testing.T, log subscriptionLog) {
	t.Helper()
	ctx := context.Background()
	if err := log.Subscribe(ctx, Subscription{ID: "a", Agent: "ada", Inbox: "inbox-a", URL: "https://agent.example/a", Secret: "s"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := log.Put(ctx, "inbox-a", "", "s1", []Record{{MessageID: "m1", Kind: "created"}, {MessageID: "m2", Kind: "created"}, {MessageID: "m3", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	later := now.Add(31 * time.Second)
	claim := func(at time.Time, want string, attempt int) Delivery {
		t.Helper()
		got, ok, err := log.Claim(ctx, at)
		if err != nil || !ok || got.Event.MessageID != want || got.Attempt != attempt {
			t.Fatalf("want %s attempt %d, got %+v ok %v err %v", want, attempt, got, ok, err)
		}
		return got
	}
	first := claim(now, "m1", 1)
	claim(later, "m1", 2)
	// The first sender gives up while the second is posting, and the second succeeds.
	if err := log.Fail(ctx, first.ID, later); err != nil {
		t.Fatal(err)
	}
	if err := log.Ack(ctx, first.ID, later); err != nil {
		t.Fatal(err)
	}
	second := claim(later, "m2", 1)
	if err := log.Retry(ctx, first.ID, later); err != nil {
		t.Fatal(err)
	}
	if err := log.Fail(ctx, first.ID, later); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := log.Claim(ctx, later); err != nil || ok {
		t.Fatalf("a late outcome for %d released %d's lease: claimed %+v err %v", first.ID, second.ID, got, err)
	}
	if err := log.Ack(ctx, second.ID, later); err != nil {
		t.Fatal(err)
	}
	claim(later, "m3", 1)
	snap, err := log.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range snap.Deliveries {
		if row.ID == first.ID && (!row.Done || row.Failed) {
			t.Fatalf("delivery the receiver took: %+v", row)
		}
	}
}

func TestPostgresRetryWaitsAcrossAReopen(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.Subscribe(ctx, Subscription{ID: "a", Agent: "ada", Inbox: "inbox-a", URL: "https://agent.example/a", Secret: "s"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "inbox-a", "", "s1", []Record{{MessageID: "m1", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	claimed, ok, err := db.Claim(ctx, now)
	if err != nil || !ok {
		t.Fatalf("claim ok %v err %v", ok, err)
	}
	if err := db.Retry(ctx, claimed.ID, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := Open(ctx, os.Getenv("INBAI_TEST_DATABASE"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, ok, err := reopened.Claim(ctx, now.Add(10*time.Minute-time.Second)); err != nil || ok {
		t.Fatalf("retry claimed early %+v err %v", got, err)
	}
	if got, ok, err := reopened.Claim(ctx, now.Add(10*time.Minute)); err != nil || !ok || got.ID != claimed.ID || got.Attempt != 2 {
		t.Fatalf("retry %+v ok %v err %v", got, ok, err)
	}
}

// TestPostgresClaimRechecksTheDelivery finishes a delivery after a claim read it as pending.
// The claim's snapshot predates the finish and it already holds the subscription row; it
// must not hand the delivery out again or leave the subscription leased. In CI the same
// window opened when one worker claimed and acked a delivery while another worker's claim,
// whose snapshot was older, was still on its way to the subscription row lock. A
// transaction holding the delivery row makes that window deterministic here.
func TestPostgresClaimRechecksTheDelivery(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.Subscribe(ctx, Subscription{ID: "a", Agent: "ada", Inbox: "inbox-a", URL: "https://agent.example/a", Secret: "s"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(ctx, "inbox-a", "", "s1", []Record{{MessageID: "m1", Kind: "created"}, {MessageID: "m2", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	var first int64
	if err := db.pool.QueryRow(ctx, `SELECT min(id) FROM webhook_delivery`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	finisher, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = finisher.Rollback(ctx) }()
	now := time.Now().Add(time.Second)
	if _, err := finisher.Exec(ctx, `UPDATE webhook_delivery SET done_at = $2 WHERE id = $1`, first, now); err != nil {
		t.Fatal(err)
	}
	type result struct {
		delivery Delivery
		ok       bool
		err      error
	}
	claimed := make(chan result, 1)
	go func() {
		delivery, ok, err := db.Claim(ctx, now)
		claimed <- result{delivery, ok, err}
	}()
	// Wait until the claim has read the delivery and waits for its row lock.
	for waited := 0; ; waited++ {
		var waiting int
		if err := db.pool.QueryRow(ctx, `
SELECT count(*) FROM pg_stat_activity
WHERE wait_event_type = 'Lock' AND query LIKE '%WITH claimed AS%' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if waited == 500 {
			t.Fatal("the claim never waited for the delivery row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := finisher.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-claimed
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.ok && got.delivery.ID == first {
		t.Fatalf("claimed delivery %d after it was delivered", first)
	}
	var leased *int64
	if err := db.pool.QueryRow(ctx, `SELECT sending_delivery FROM webhook_subscription WHERE id = 'a'`).Scan(&leased); err != nil {
		t.Fatal(err)
	}
	if !got.ok && leased != nil {
		t.Fatalf("nothing was claimed but the subscription is leased to %d", *leased)
	}
	if !got.ok {
		got.delivery, got.ok, got.err = db.Claim(ctx, now)
	}
	if got.err != nil || !got.ok || got.delivery.ID == first || got.delivery.Event.MessageID != "m2" || got.delivery.Attempt != 1 {
		t.Fatalf("next claim %+v ok %v err %v", got.delivery, got.ok, got.err)
	}
}

// TestPostgresReplicasNeverShareASubscription claims from two pools at once, standing in for
// two inbaid processes, and checks no subscription ever has two deliveries in flight and
// each subscription's events go out in order.
func TestPostgresReplicasNeverShareASubscription(t *testing.T) {
	first := openTestDB(t)
	ctx := context.Background()
	second, err := Open(ctx, os.Getenv("INBAI_TEST_DATABASE"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	const subs, perSub = 3, 15
	for s := range subs {
		inbox := fmt.Sprintf("inbox-%d", s)
		if err := first.Subscribe(ctx, Subscription{ID: fmt.Sprintf("sub-%d", s), Agent: "ada", Inbox: inbox, URL: "https://agent.example/", Secret: "s"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := range perSub {
		for s := range subs {
			if err := first.Put(ctx, fmt.Sprintf("inbox-%d", s), "", "s", []Record{{MessageID: fmt.Sprintf("m%d", i), Kind: "created"}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var mu sync.Mutex
	inflight := map[string]bool{}
	claimed := map[int64]bool{}
	order := map[string][]int64{}
	var problems []string
	var wg sync.WaitGroup
	deadline := time.Now().Add(20 * time.Second)
	for w := range 8 {
		db := first
		if w%2 == 1 {
			db = second
		}
		wg.Go(func() {
			for time.Now().Before(deadline) {
				mu.Lock()
				left := subs*perSub - len(order["sub-0"]) - len(order["sub-1"]) - len(order["sub-2"])
				mu.Unlock()
				// A delivery claimed twice makes the total overshoot; stop then too.
				if left <= 0 {
					return
				}
				delivery, ok, err := db.Claim(ctx, time.Now().Add(time.Second))
				if err != nil {
					mu.Lock()
					problems = append(problems, err.Error())
					mu.Unlock()
					return
				}
				if !ok {
					time.Sleep(time.Millisecond)
					continue
				}
				mu.Lock()
				if claimed[delivery.ID] {
					problems = append(problems, fmt.Sprintf("delivery %d claimed twice", delivery.ID))
				}
				claimed[delivery.ID] = true
				if inflight[delivery.SubscriptionID] {
					problems = append(problems, "two deliveries in flight for "+delivery.SubscriptionID)
				}
				inflight[delivery.SubscriptionID] = true
				order[delivery.SubscriptionID] = append(order[delivery.SubscriptionID], delivery.Event.ID)
				mu.Unlock()
				time.Sleep(time.Millisecond)
				mu.Lock()
				inflight[delivery.SubscriptionID] = false
				mu.Unlock()
				if err := db.Ack(ctx, delivery.ID, time.Now()); err != nil {
					mu.Lock()
					problems = append(problems, err.Error())
					mu.Unlock()
					return
				}
			}
		})
	}
	wg.Wait()
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for s := range subs {
		ids := order[fmt.Sprintf("sub-%d", s)]
		if len(ids) != perSub {
			t.Fatalf("sub-%d got %d deliveries", s, len(ids))
		}
		for i := 1; i < len(ids); i++ {
			if ids[i] <= ids[i-1] {
				t.Fatalf("sub-%d out of order: %v", s, ids)
			}
		}
	}
}
