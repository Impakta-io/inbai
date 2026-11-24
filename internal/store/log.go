// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
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
// Agent is the agent that created it. Operator is true when an operator created it.
// Disabled is why deliveries stopped. Empty means the subscription is active.
type Subscription struct {
	ID       string `json:"id"`
	Agent    string `json:"agent"`
	Inbox    string `json:"inbox"`
	URL      string `json:"url"`
	Secret   string `json:"secret"`
	Operator bool   `json:"operator"`
	Disabled string `json:"disabled,omitempty"`
}

// ErrSubscriptionLimit is returned by Subscribe when the agent already has the limit on that inbox.
var ErrSubscriptionLimit = errors.New("webhook subscription limit reached")

// Delivery is one webhook attempt claimed by a worker.
// Agent and Operator name who created the subscription, so the worker can check access first.
type Delivery struct {
	ID             int64
	Attempt        int
	SubscriptionID string
	Agent          string
	Operator       bool
	URL            string
	Secret         string
	Event          Record
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
	Head(context.Context, string) (int64, error)
	// Subscribe stores a subscription. A limit above zero caps the subscriptions one
	// agent has on one inbox; at the cap it returns ErrSubscriptionLimit and stores nothing.
	Subscribe(context.Context, Subscription, int) error
	Subscription(context.Context, string) (Subscription, bool, error)
	Subscriptions(context.Context, string) ([]Subscription, error)
	// Unsubscribe removes a subscription and every delivery queued for it.
	Unsubscribe(context.Context, string) error
	// Disable stops a subscription. Its pending deliveries fail with the reason and it gets no new ones.
	Disable(context.Context, string, string, time.Time) error
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
