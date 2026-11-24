// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestWebhookPolicyDefaultsToPublicOnly(t *testing.T) {
	cases := map[string]bool{"": false, "false": false, "0": false, "true": true, "1": true}
	for value, want := range cases {
		feed := events.New(&store.Memory{})
		if err := applyWebhookPolicy(feed, only("INBAI_WEBHOOK_ALLOW_PRIVATE", value)); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if feed.AllowPrivate() != want {
			t.Errorf("%q: allow %v, want %v", value, feed.AllowPrivate(), want)
		}
	}
	if err := applyWebhookPolicy(events.New(&store.Memory{}), only("INBAI_WEBHOOK_ALLOW_PRIVATE", "maybe")); err == nil {
		t.Fatal("a bad value must be an error")
	}
}

func only(name, value string) func(string) string {
	return func(key string) string {
		if key == name {
			return value
		}
		return ""
	}
}

func TestWebhookWorkersComeFromTheEnvironment(t *testing.T) {
	feed := events.New(&store.Memory{})
	if err := applyWebhookPolicy(feed, only("", "")); err != nil || feed.Workers() != events.DefaultWebhookWorkers {
		t.Fatalf("default workers %d err %v", feed.Workers(), err)
	}
	if err := applyWebhookPolicy(feed, only("INBAI_WEBHOOK_WORKERS", "3")); err != nil || feed.Workers() != 3 {
		t.Fatalf("workers %d err %v", feed.Workers(), err)
	}
	for _, bad := range []string{"0", "-1", "257", "many"} {
		if err := applyWebhookPolicy(events.New(&store.Memory{}), only("INBAI_WEBHOOK_WORKERS", bad)); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
}

func TestPruneRetentionComesFromTheEnvironment(t *testing.T) {
	day := 24 * time.Hour
	keep, err := pruneRetention(only("", ""))
	if err != nil || keep.Events != 30*day || keep.Deliveries != 7*day || keep.DoneJobs != day || keep.FailedJobs != 7*day {
		t.Fatalf("defaults %+v err %v", keep, err)
	}
	env := map[string]string{"INBAI_EVENT_RETENTION_DAYS": "0", "INBAI_DELIVERY_RETENTION_DAYS": "2"}
	keep, err = pruneRetention(func(name string) string { return env[name] })
	if err != nil || keep.Events != 0 || keep.Deliveries != 2*day {
		t.Fatalf("set %+v err %v", keep, err)
	}
	if _, err := pruneRetention(only("INBAI_EVENT_RETENTION_DAYS", "-3")); err == nil {
		t.Fatal("a negative period was accepted")
	}
}
