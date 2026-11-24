// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/store"
)

const maxAttempts = 5

// Feed turns JMAP changes into an ordered log and signed webhook deliveries.
type Feed struct {
	log      store.Log
	now      func() time.Time
	backoff  func(attempt int) time.Duration
	appended atomic.Int64
	posted   atomic.Int64

	allowPrivate bool
	client       *http.Client
}

// New returns a feed over a durable log.
func New(log store.Log) *Feed {
	return &Feed{
		log:    log,
		client: NewWebhookClient(false),
		now:    time.Now,
		backoff: func(attempt int) time.Duration {
			shift := attempt - 1
			if shift > 4 {
				shift = 4
			}
			return time.Second << shift
		},
	}
}

// SetAllowPrivate lets an operator allow http and private webhook targets.
// Call it before delivery starts. It is never set from a request.
func (f *Feed) SetAllowPrivate(allow bool) {
	f.allowPrivate = allow
	f.client = NewWebhookClient(allow)
}

// AllowPrivate reports whether the operator allowed private webhook targets.
func (f *Feed) AllowPrivate() bool {
	return f.allowPrivate
}

// Sign returns the HMAC-SHA256 signature header value for a webhook body.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// ValidSignature reports whether header matches the body.
func ValidSignature(secret string, body []byte, header string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(header))
}

// Remember binds an address to an inbox so an ingest hint can find it.
func (f *Feed) Remember(ctx context.Context, inbox, address string) error {
	return f.log.Remember(ctx, inbox, address)
}

// Ping checks the log.
func (f *Feed) Ping(ctx context.Context) error {
	return f.log.Ping(ctx)
}

// Sync asks the mail backend for changes since the stored cursor and appends them.
// The first call for an inbox stores the current state and does not invent history.
func (f *Feed) Sync(ctx context.Context, watch mailbackend.Watcher, inbox string) error {
	state, ok, err := f.log.State(ctx, inbox)
	if err != nil {
		return err
	}
	if !ok {
		state, err = watch.MailState(ctx, inbox)
		if err != nil {
			return err
		}
		return f.log.Put(ctx, inbox, "", state, nil)
	}
	for {
		delta, err := watch.MailChanges(ctx, inbox, state)
		if err != nil {
			return err
		}
		records := make([]store.Record, 0, len(delta.Created)+len(delta.Destroyed))
		for _, id := range delta.Created {
			records = append(records, store.Record{MessageID: id, Kind: "created"})
		}
		for _, id := range delta.Destroyed {
			records = append(records, store.Record{MessageID: id, Kind: "destroyed"})
		}
		if err := f.log.Put(ctx, inbox, "", delta.State, records); err != nil {
			return err
		}
		f.appended.Add(int64(len(records)))
		state = delta.State
		if !delta.More {
			return nil
		}
	}
}

// Sweep syncs every inbox the log knows about.
// A failure on one inbox does not stop the others. It returns every failure, each naming its inbox.
// It stops early only when ctx ends.
func (f *Feed) Sweep(ctx context.Context, watch mailbackend.Watcher) error {
	inboxes, err := f.log.Inboxes(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, inbox := range inboxes {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := f.Sync(ctx, watch, inbox); err != nil {
			errs = append(errs, fmt.Errorf("inbox %s: %w", inbox, err))
		}
	}
	return errors.Join(errs...)
}

// Forget stops sweeping an inbox and drops its address binding. Events already logged stay.
func (f *Feed) Forget(ctx context.Context, inbox string) error {
	return f.log.Forget(ctx, inbox)
}

// Since returns events after a consumer cursor.
func (f *Feed) Since(ctx context.Context, inbox string, after int64, limit int) ([]store.Record, error) {
	return f.log.Since(ctx, inbox, after, limit)
}

// InboxByAddress resolves a recipient from an ingest hint.
func (f *Feed) InboxByAddress(ctx context.Context, address string) (string, bool, error) {
	return f.log.InboxByAddress(ctx, address)
}

// Subscribe registers a webhook. The returned secret is shown once.
func (f *Feed) Subscribe(ctx context.Context, agent, inbox, url string) (string, string, error) {
	id, err := randomID()
	if err != nil {
		return "", "", err
	}
	secret, err := randomID()
	if err != nil {
		return "", "", err
	}
	if err := f.log.Subscribe(ctx, store.Subscription{ID: id, Agent: agent, Inbox: inbox, URL: url, Secret: secret}); err != nil {
		return "", "", err
	}
	return id, secret, nil
}

// DeliverOnce posts one due webhook. The boolean is false when nothing is due.
func (f *Feed) DeliverOnce(ctx context.Context, client *http.Client) (bool, error) {
	delivery, ok, err := f.log.Claim(ctx, f.now())
	if err != nil || !ok {
		return false, err
	}
	if client == nil {
		client = f.client
	}
	body, err := json.Marshal(delivery.Event)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.URL, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Inbai-Signature", Sign(delivery.Secret, body))
	res, err := client.Do(req)
	if err != nil {
		return true, f.failOrRetry(ctx, delivery)
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		f.posted.Add(1)
		return true, f.log.Ack(ctx, delivery.ID, f.now())
	}
	return true, f.failOrRetry(ctx, delivery)
}

func (f *Feed) failOrRetry(ctx context.Context, delivery store.Delivery) error {
	if delivery.Attempt >= maxAttempts {
		return f.log.Fail(ctx, delivery.ID, f.now())
	}
	return f.log.Retry(ctx, delivery.ID, f.now().Add(f.backoff(delivery.Attempt)))
}

// Run sweeps mailboxes and posts webhooks until the context ends.
func (f *Feed) Run(ctx context.Context, watch mailbackend.Watcher, client *http.Client) {
	sweep := time.NewTicker(30 * time.Second)
	post := time.NewTicker(time.Second)
	defer sweep.Stop()
	defer post.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			if watch != nil {
				_ = f.Sweep(ctx, watch)
			}
		case <-post.C:
			for range 20 {
				ok, err := f.DeliverOnce(ctx, client)
				if err != nil || !ok {
					break
				}
			}
		}
	}
}

// RunWebhooks claims and posts due webhook deliveries until the context ends.
func (f *Feed) RunWebhooks(ctx context.Context, client *http.Client) {
	post := time.NewTicker(time.Second)
	defer post.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-post.C:
			for range 20 {
				ok, err := f.DeliverOnce(ctx, client)
				if err != nil || !ok {
					break
				}
			}
		}
	}
}

// Counters returns events appended and webhooks posted.
func (f *Feed) Counters() (int64, int64) {
	return f.appended.Load(), f.posted.Load()
}

// Suppress records a recipient that must not be mailed again. The first reason is kept.
func (f *Feed) Suppress(ctx context.Context, address, reason string) error {
	return f.log.Suppress(ctx, address, reason)
}

// Suppressed reports whether an address is on the suppression list.
func (f *Feed) Suppressed(ctx context.Context, address string) (string, bool, error) {
	return f.log.Suppressed(ctx, address)
}

// Suppressions returns the list.
func (f *Feed) Suppressions(ctx context.Context) ([]store.Suppression, error) {
	return f.log.Suppressions(ctx)
}

func randomID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("random id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
