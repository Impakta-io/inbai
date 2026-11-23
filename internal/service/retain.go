// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

const retentionServer = "server"

// SetRetention stores how many days an inbox keeps mail.
// An empty inbox sets the server default. Zero keeps mail. Only an operator may do this.
func (s *Service) SetRetention(caller Caller, inbox string, days int) error {
	if !caller.Operator {
		return ErrForbidden
	}
	if days < 0 {
		return fmt.Errorf("%w: retention days must be zero or greater", ErrInvalid)
	}
	scope := retentionScope(inbox)
	if s.db != nil {
		if err := s.db.SaveRetention(context.Background(), scope, days); err != nil {
			return err
		}
	} else {
		s.mu.Lock()
		if s.keep == nil {
			s.keep = map[string]int{}
		}
		s.keep[scope] = days
		s.mu.Unlock()
	}
	target := scope
	return s.record(context.Background(), caller, "retention.set", fmt.Sprintf("%s %d", target, days))
}

// RetentionDays returns the days stored for an inbox, or the server default when the inbox has no row.
// set reports whether a row exists for that scope.
func (s *Service) RetentionDays(caller Caller, inbox string) (int, bool, error) {
	if !caller.Operator {
		return 0, false, ErrForbidden
	}
	return s.policy(context.Background(), retentionScope(inbox))
}

// Sweep deletes mail older than the retention period. fallback is the server period when none is stored.
// Zero keeps mail. A message with no received time is kept. Only an operator may run it.
func (s *Service) Sweep(ctx context.Context, caller Caller, fallback int) (int, error) {
	if !caller.Operator {
		return 0, ErrForbidden
	}
	if fallback < 0 {
		return 0, fmt.Errorf("%w: retention days must be zero or greater", ErrInvalid)
	}
	serverDays, set, err := s.policy(ctx, retentionServer)
	if err != nil {
		return 0, err
	}
	if !set {
		serverDays = fallback
	}
	if serverDays == 0 && !s.hasInboxRetention(ctx) {
		return 0, nil
	}
	var after string
	deleted := 0
	for {
		accounts, next, err := s.mail.ListInboxes(ctx, mailbackend.Page{After: after, Limit: mailbackend.MaxLimit})
		if err != nil {
			return deleted, err
		}
		for _, account := range accounts {
			days, ok, err := s.policy(ctx, retentionScope(account.ID))
			if err != nil {
				return deleted, err
			}
			if !ok {
				days = serverDays
			}
			if days <= 0 {
				continue
			}
			n, err := s.expire(ctx, account.ID, s.now().UTC().AddDate(0, 0, -days))
			if err != nil {
				return deleted, err
			}
			deleted += n
			if n > 0 {
				if err := s.record(ctx, caller, "retention", fmt.Sprintf("%s %d", account.ID, n)); err != nil {
					return deleted, err
				}
			}
		}
		if next == "" || next == after {
			return deleted, nil
		}
		after = next
	}
}

func (s *Service) expire(ctx context.Context, accountID string, cutoff time.Time) (int, error) {
	var after string
	var doomed []string
	for {
		messages, next, err := s.mail.List(ctx, accountID, mailbackend.Page{After: after, Limit: mailbackend.MaxLimit})
		if err != nil {
			return 0, err
		}
		for _, message := range messages {
			if !message.ReceivedAt.IsZero() && message.ReceivedAt.Before(cutoff) {
				doomed = append(doomed, message.ID)
			}
		}
		if next == "" || next == after {
			break
		}
		after = next
	}
	for _, id := range doomed {
		if err := s.mail.Delete(ctx, accountID, id); err != nil {
			return 0, err
		}
	}
	return len(doomed), nil
}

func (s *Service) hasInboxRetention(ctx context.Context) bool {
	if s.db != nil {
		rows, err := s.db.ListRetention(ctx)
		if err != nil {
			return true
		}
		for _, row := range rows {
			if row.Scope != retentionServer && row.Days > 0 {
				return true
			}
		}
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for scope, days := range s.keep {
		if scope != retentionServer && days > 0 {
			return true
		}
	}
	return false
}

func (s *Service) policy(ctx context.Context, scope string) (int, bool, error) {
	if s.db != nil {
		return s.db.Retention(ctx, scope)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	days, ok := s.keep[scope]
	return days, ok, nil
}

func retentionScope(inbox string) string {
	if inbox == "" {
		return retentionServer
	}
	return "inbox:" + inbox
}
