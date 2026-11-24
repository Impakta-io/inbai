// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestWebhookPolicyDefaultsToPublicOnly(t *testing.T) {
	cases := map[string]bool{"": false, "false": false, "0": false, "true": true, "1": true}
	for value, want := range cases {
		feed := events.New(&store.Memory{})
		if err := applyWebhookPolicy(feed, func(string) string { return value }); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if feed.AllowPrivate() != want {
			t.Errorf("%q: allow %v, want %v", value, feed.AllowPrivate(), want)
		}
	}
	if err := applyWebhookPolicy(events.New(&store.Memory{}), func(string) string { return "maybe" }); err == nil {
		t.Fatal("a bad value must be an error")
	}
}
