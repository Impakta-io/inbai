// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

// Package events records mail changes and delivers them to subscribers.
// Delivery is at least once and ordered per inbox. Webhooks are posted by a bounded
// pool, one at a time per subscription, and retried on a schedule stored with each delivery.
package events
