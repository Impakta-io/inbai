// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/store"
)

// ErrUnavailable is returned when the event log is not configured.
var ErrUnavailable = errorsUnavailable{}

type errorsUnavailable struct{}

func (errorsUnavailable) Error() string { return "event store is not configured" }

// SetFeed attaches the durable event log.
func (s *Service) SetFeed(feed *events.Feed) {
	s.feed = feed
}

// BindInbox records the address and stores the mailbox's current JMAP state.
func (s *Service) BindInbox(ctx context.Context, inbox, address string) error {
	if s.feed == nil {
		return nil
	}
	if err := s.feed.Remember(ctx, inbox, address); err != nil {
		return err
	}
	if _, ok := s.mail.(mailbackend.Watcher); !ok {
		return nil
	}
	return s.sync(ctx, inbox)
}

// Ingest is the Stalwart hint. It syncs each recipient this server already knows.
func (s *Service) Ingest(ctx context.Context, recipients []string) (int, error) {
	if s.feed == nil {
		return 0, fmt.Errorf("%w", ErrUnavailable)
	}
	synced := 0
	seen := map[string]struct{}{}
	for _, recipient := range recipients {
		inbox, ok, err := s.feed.InboxByAddress(ctx, recipient)
		if err != nil {
			return synced, err
		}
		if !ok {
			continue
		}
		if _, dup := seen[inbox]; dup {
			continue
		}
		seen[inbox] = struct{}{}
		if err := s.sync(ctx, inbox); err != nil {
			return synced, err
		}
		synced++
	}
	return synced, nil
}

// Notice is one Stalwart webhook event the server knows how to use.
type Notice struct {
	Type    string
	To      []string
	Details string
}

// Observe applies ingest hints and permanent delivery failures.
// A temporary failure does not suppress the recipient.
func (s *Service) Observe(ctx context.Context, notices []Notice) (int, int, error) {
	if s.feed == nil {
		return 0, 0, fmt.Errorf("%w", ErrUnavailable)
	}
	var recipients []string
	suppressed := 0
	for _, notice := range notices {
		switch {
		case strings.HasPrefix(notice.Type, "message-ingest"):
			recipients = append(recipients, notice.To...)
		case notice.Type == "delivery.dsn-perm-fail":
			reason := "delivery.dsn-perm-fail"
			if details := strings.TrimSpace(notice.Details); details != "" {
				reason += ": " + details
			}
			if len(reason) > 240 {
				reason = reason[:240]
			}
			for _, address := range notice.To {
				address = strings.ToLower(strings.TrimSpace(address))
				if address == "" || !strings.Contains(address, "@") {
					continue
				}
				_, already, err := s.feed.Suppressed(ctx, address)
				if err != nil {
					return 0, suppressed, err
				}
				if already {
					continue
				}
				if err := s.feed.Suppress(ctx, address, reason); err != nil {
					return 0, suppressed, err
				}
				suppressed++
			}
		}
	}
	synced, err := s.Ingest(ctx, recipients)
	return synced, suppressed, err
}

// Suppressions returns the hard-bounce list. Only an operator may read it.
func (s *Service) Suppressions(ctx context.Context, caller Caller) ([]store.Suppression, error) {
	if !caller.Operator {
		return nil, ErrForbidden
	}
	if s.feed == nil {
		return nil, fmt.Errorf("%w", ErrUnavailable)
	}
	list, err := s.feed.Suppressions(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []store.Suppression{}
	}
	return list, nil
}

// Events returns the feed for one inbox after a consumer cursor.
// An agent needs a read grant. The log is not read when the grant is missing.
func (s *Service) Events(ctx context.Context, caller Caller, inbox string, after int64, timeout time.Duration) ([]store.Record, error) {
	if err := s.permit(ctx, caller, inbox, PermRead); err != nil {
		return nil, err
	}
	if s.feed == nil {
		return nil, fmt.Errorf("%w", ErrUnavailable)
	}
	if timeout < 0 {
		timeout = 0
	}
	if timeout > 25*time.Second {
		timeout = 25 * time.Second
	}
	deadline := s.now().Add(timeout)
	for {
		records, err := s.feed.Since(ctx, inbox, after, 50)
		if err != nil || len(records) > 0 || !s.now().Before(deadline) {
			if records == nil {
				records = []store.Record{}
			}
			return records, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// SubscribeWebhook registers a signed webhook for one inbox.
func (s *Service) SubscribeWebhook(ctx context.Context, caller Caller, inbox, rawURL string) (string, string, error) {
	if err := s.permit(ctx, caller, inbox, PermRead); err != nil {
		return "", "", err
	}
	if s.feed == nil {
		return "", "", fmt.Errorf("%w", ErrUnavailable)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", "", fmt.Errorf("%w: webhook url must be http or https", ErrInvalid)
	}
	agent := caller.Agent
	if caller.Operator {
		agent = "operator"
	}
	return s.feed.Subscribe(ctx, agent, inbox, rawURL)
}

// HasFeed reports whether a durable event log is attached.
func (s *Service) HasFeed() bool {
	return s.feed != nil
}

// Ready pings the event log when one is configured.
func (s *Service) Ready(ctx context.Context) error {
	if s.feed == nil {
		return nil
	}
	return s.feed.Ping(ctx)
}

// Metrics returns Prometheus text for the event feed.
func (s *Service) Metrics() string {
	var appended, posted int64
	if s.feed != nil {
		appended, posted = s.feed.Counters()
	}
	return fmt.Sprintf(`# TYPE inbai_events_appended_total counter
inbai_events_appended_total %d
# TYPE inbai_webhooks_delivered_total counter
inbai_webhooks_delivered_total %d
`, appended, posted)
}

func (s *Service) sync(ctx context.Context, inbox string) error {
	watch, ok := s.mail.(mailbackend.Watcher)
	if !ok {
		return fmt.Errorf("mail backend has no changes")
	}
	return s.feed.Sync(ctx, watch, inbox)
}
