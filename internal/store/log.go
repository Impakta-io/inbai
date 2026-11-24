// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

// Record is one mail change stored for an inbox, in the order it was observed.
type Record struct {
	ID        int64  `json:"id"`
	Inbox     string `json:"inbox"`
	MessageID string `json:"messageId"`
	Kind      string `json:"kind"`
}

// Subscription is a consumer webhook. The secret is shown once when it is created.
type Subscription struct {
	ID     string `json:"id"`
	Agent  string `json:"agent"`
	Inbox  string `json:"inbox"`
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

// Delivery is one webhook attempt claimed by a worker.
type Delivery struct {
	ID      int64
	Attempt int
	URL     string
	Secret  string
	Event   Record
}

// Log is the durable event record. A process restart must not drop it.
type Log interface {
	Ping(context.Context) error
	Remember(context.Context, string, string) error
	State(context.Context, string) (string, bool, error)
	InboxByAddress(context.Context, string) (string, bool, error)
	Inboxes(context.Context) ([]string, error)
	Forget(context.Context, string) error
	Put(context.Context, string, string, string, []Record) error
	Since(context.Context, string, int64, int) ([]Record, error)
	Subscribe(context.Context, Subscription) error
	Claim(context.Context, time.Time) (Delivery, bool, error)
	Ack(context.Context, int64, time.Time) error
	Retry(context.Context, int64, time.Time) error
	Fail(context.Context, int64, time.Time) error
	Suppress(context.Context, string, string) error
	Suppressed(context.Context, string) (string, bool, error)
	Suppressions(context.Context) ([]Suppression, error)
}

// Suppression is one recipient that must not be mailed again.
type Suppression struct {
	Address string `json:"address"`
	Reason  string `json:"reason"`
}
