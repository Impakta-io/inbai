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
	"log/slog"
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
	gate         Gate

	// logger receives cursor recovery notices. Nil means slog.Default().
	logger *slog.Logger
}

// Gate decides, before each webhook is sent, whether the subscription's creator may still
// see the inbox. The feed does not read grants itself. internal/service implements it.
type Gate interface {
	// MayDeliver reports whether a delivery for inbox may go to a subscription created by
	// agent, or by an operator when operator is true. reason says why not.
	MayDeliver(ctx context.Context, agent string, operator bool, inbox string) (ok bool, reason string, err error)
}

// SetGate installs the access check run on every claimed delivery. Call it before delivery starts.
func (f *Feed) SetGate(gate Gate) {
	f.gate = gate
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
// When the backend can no longer report changes since the cursor, Sync recovers:
// see recoverCursor.
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
		if errors.Is(err, mailbackend.ErrChangesLost) {
			return f.recoverCursor(ctx, watch, inbox, state, err)
		}
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

// recoverCursor handles a cursor the backend can no longer report changes from.
// An inbox the backend says is gone is forgotten. Otherwise the cursor moves to
// the current state and one resync event, with no message id, tells consumers
// that changes in between may be missing and the inbox should be listed again.
func (f *Feed) recoverCursor(ctx context.Context, watch mailbackend.Watcher, inbox, state string, cause error) error {
	if check, ok := watch.(mailbackend.InboxChecker); ok {
		exists, err := check.InboxExists(ctx, inbox)
		if err != nil {
			return fmt.Errorf("%w; checking whether the inbox exists: %w", cause, err)
		}
		if !exists {
			if err := f.log.Forget(ctx, inbox); err != nil {
				return err
			}
			f.notices().Warn("event cursor dropped: the inbox no longer exists on the mail server", "inbox", inbox)
			return nil
		}
	}
	current, err := watch.MailState(ctx, inbox)
	if err != nil {
		return fmt.Errorf("%w; reading the current state: %w", cause, err)
	}
	if current == state {
		return cause
	}
	if err := f.log.Put(ctx, inbox, "", current, []store.Record{{Kind: "resync"}}); err != nil {
		return err
	}
	f.appended.Add(1)
	f.notices().Warn("event cursor moved to the current state: the mail server cannot report changes since the stored one, so events in between may be missing",
		"inbox", inbox, "from", state, "to", current, "cause", cause.Error())
	return nil
}

func (f *Feed) notices() *slog.Logger {
	if f.logger != nil {
		return f.logger
	}
	return slog.Default()
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

// Head returns the id of the newest event for an inbox, or 0 when it has none.
func (f *Feed) Head(ctx context.Context, inbox string) (int64, error) {
	return f.log.Head(ctx, inbox)
}

// InboxByAddress resolves a recipient from an ingest hint.
func (f *Feed) InboxByAddress(ctx context.Context, address string) (string, bool, error) {
	return f.log.InboxByAddress(ctx, address)
}

// Subscribe registers a webhook. sub names the creator, the inbox, and the url; the id and
// secret are generated. limit caps one agent's subscriptions on the inbox; zero is no cap.
// The returned secret is shown once.
func (f *Feed) Subscribe(ctx context.Context, sub store.Subscription, limit int) (string, string, error) {
	id, err := randomID()
	if err != nil {
		return "", "", err
	}
	secret, err := randomID()
	if err != nil {
		return "", "", err
	}
	sub.ID, sub.Secret, sub.Disabled = id, secret, ""
	if err := f.log.Subscribe(ctx, sub, limit); err != nil {
		return "", "", err
	}
	return id, secret, nil
}

// Subscription returns one webhook by id.
func (f *Feed) Subscription(ctx context.Context, id string) (store.Subscription, bool, error) {
	return f.log.Subscription(ctx, id)
}

// Subscriptions returns every webhook on an inbox.
func (f *Feed) Subscriptions(ctx context.Context, inbox string) ([]store.Subscription, error) {
	return f.log.Subscriptions(ctx, inbox)
}

// Unsubscribe removes a webhook and the deliveries still queued for it.
func (f *Feed) Unsubscribe(ctx context.Context, id string) error {
	return f.log.Unsubscribe(ctx, id)
}

// DeliverOnce posts one due webhook. The boolean is false when nothing is due.
// When a gate is set, it is asked first. A refused delivery is not sent: it fails with
// the gate's reason and its subscription is disabled, so it gets no further deliveries.
func (f *Feed) DeliverOnce(ctx context.Context, client *http.Client) (bool, error) {
	delivery, ok, err := f.log.Claim(ctx, f.now())
	if err != nil || !ok {
		return false, err
	}
	if f.gate != nil {
		allowed, reason, err := f.gate.MayDeliver(ctx, delivery.Agent, delivery.Operator, delivery.Event.Inbox)
		if err != nil {
			return true, errors.Join(err, f.failOrRetry(ctx, delivery))
		}
		if !allowed {
			return true, f.log.Disable(ctx, delivery.SubscriptionID, reason, f.now())
		}
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
