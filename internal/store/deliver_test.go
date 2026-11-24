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
				if left == 0 {
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
