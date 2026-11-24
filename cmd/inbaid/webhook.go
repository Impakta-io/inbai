// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Impakta-io/inbai/internal/events"
)

// applyWebhookPolicy reads INBAI_WEBHOOK_ALLOW_PRIVATE and INBAI_WEBHOOK_WORKERS.
// INBAI_WEBHOOK_ALLOW_PRIVATE is off unless the operator sets it, and it lets
// webhooks use http and private addresses. INBAI_WEBHOOK_WORKERS is how many
// webhooks this process posts at the same time, from 1 to maxWebhookWorkers.
func applyWebhookPolicy(feed *events.Feed, getenv func(string) string) error {
	if getenv == nil {
		return nil
	}
	if raw := strings.TrimSpace(getenv("INBAI_WEBHOOK_WORKERS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxWebhookWorkers {
			return fmt.Errorf("INBAI_WEBHOOK_WORKERS must be a whole number from 1 to %d", maxWebhookWorkers)
		}
		feed.SetWorkers(n)
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

// maxWebhookWorkers bounds INBAI_WEBHOOK_WORKERS. Each worker can hold one open
// connection to a receiver.
const maxWebhookWorkers = 256
