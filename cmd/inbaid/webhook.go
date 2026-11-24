// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Impakta-io/inbai/internal/events"
)

// applyWebhookPolicy reads INBAI_WEBHOOK_ALLOW_PRIVATE. It is off unless the
// operator sets it, and it lets webhooks use http and private addresses.
func applyWebhookPolicy(feed *events.Feed, getenv func(string) string) error {
	if getenv == nil {
		return nil
	}
	raw := strings.TrimSpace(getenv("INBAI_WEBHOOK_ALLOW_PRIVATE"))
	if raw == "" {
		return nil
	}
	allow, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("INBAI_WEBHOOK_ALLOW_PRIVATE must be true or false")
	}
	feed.SetAllowPrivate(allow)
	return nil
}
