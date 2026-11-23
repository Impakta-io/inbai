// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

const maxAllowlist = 100

// SetAllowlist replaces the sender allowlist for one inbox.
// An empty list removes it. An operator may do this, and so may an agent with manage on that inbox.
// A read grant does not. The list is a signal on a later message read. It does not reject mail.
func (s *Service) SetAllowlist(ctx context.Context, caller Caller, inbox string, senders []string) error {
	if inbox == "" || strings.ContainsAny(inbox, " \t") {
		return fmt.Errorf("%w: allowlist needs an inbox", ErrInvalid)
	}
	if err := s.permitManage(ctx, caller, inbox); err != nil {
		return err
	}
	if len(senders) > maxAllowlist {
		return fmt.Errorf("%w: an allowlist holds at most %d senders", ErrInvalid, maxAllowlist)
	}
	normalized, err := normalizeSenders(senders)
	if err != nil {
		return err
	}
	if err := s.storeAllowlist(ctx, inbox, normalized); err != nil {
		return err
	}
	return s.record(ctx, caller, "allowlist.set", inbox)
}

// Allowlist returns the stored senders. set is false when the inbox has no list.
// An operator, a read grant, or a manage grant on that inbox may read it.
func (s *Service) Allowlist(ctx context.Context, caller Caller, inbox string) ([]string, bool, error) {
	if inbox == "" || strings.ContainsAny(inbox, " \t") {
		return nil, false, fmt.Errorf("%w: allowlist needs an inbox", ErrInvalid)
	}
	if err := s.permitAllowlistRead(ctx, caller, inbox); err != nil {
		return nil, false, err
	}
	senders, set, err := s.allowlist(ctx, inbox)
	if err != nil || senders == nil {
		senders = []string{}
	}
	return senders, set, err
}

func (s *Service) permitManage(ctx context.Context, caller Caller, inbox string) error {
	if caller.Operator {
		return nil
	}
	return s.permit(ctx, caller, inbox, PermManage)
}

func (s *Service) permitAllowlistRead(ctx context.Context, caller Caller, inbox string) error {
	if caller.Operator {
		return nil
	}
	ok, err := s.allowed(ctx, caller, inbox, PermRead)
	if err != nil || ok {
		return err
	}
	return s.permit(ctx, caller, inbox, PermManage)
}

func (s *Service) applyAllowlist(ctx context.Context, inbox string, msg *mailbackend.Message) error {
	senders, set, err := s.allowlist(ctx, inbox)
	if err != nil || !set {
		return err
	}
	match := senderMatches(msg.From, senders)
	msg.SenderAllowed = &match
	return nil
}

func (s *Service) storeAllowlist(ctx context.Context, inbox string, senders []string) error {
	if s.db != nil {
		if len(senders) == 0 {
			return s.db.DeleteAllowlist(ctx, inbox)
		}
		return s.db.SaveAllowlist(ctx, inbox, senders)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.allow == nil {
		s.allow = map[string][]string{}
	}
	if len(senders) == 0 {
		delete(s.allow, inbox)
		return nil
	}
	s.allow[inbox] = append([]string(nil), senders...)
	return nil
}

func (s *Service) allowlist(ctx context.Context, inbox string) ([]string, bool, error) {
	if s.db != nil {
		return s.db.Allowlist(ctx, inbox)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	senders, ok := s.allow[inbox]
	if !ok {
		return nil, false, nil
	}
	return append([]string(nil), senders...), true, nil
}

func normalizeSenders(raw []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range raw {
		one, err := normalizeSender(item)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[one]; ok {
			continue
		}
		seen[one] = struct{}{}
		out = append(out, one)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeSender(raw string) (string, error) {
	sender := strings.ToLower(strings.TrimSpace(raw))
	if sender == "" || strings.ContainsAny(sender, " \t<>/,\\") {
		return "", fmt.Errorf("%w: %q is not an address or a domain", ErrInvalid, raw)
	}
	local, domain, addressed := strings.Cut(sender, "@")
	if addressed {
		if local == "" || strings.Contains(domain, "@") || !domainName(domain) {
			return "", fmt.Errorf("%w: %q is not an address or a domain", ErrInvalid, raw)
		}
		return sender, nil
	}
	if !domainName(sender) {
		return "", fmt.Errorf("%w: %q is not an address or a domain", ErrInvalid, raw)
	}
	return sender, nil
}

func domainName(value string) bool {
	if value == "" || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.Contains(value, "..") {
		return false
	}
	for _, r := range value {
		if r != '.' && r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func senderMatches(from string, senders []string) bool {
	addr := senderAddress(from)
	if !strings.Contains(addr, "@") {
		return false
	}
	domain := domainOf(addr)
	for _, item := range senders {
		if item == addr || item == domain {
			return true
		}
	}
	return false
}

func senderAddress(from string) string {
	from = strings.TrimSpace(from)
	if start := strings.LastIndex(from, "<"); start >= 0 && strings.HasSuffix(from, ">") {
		from = strings.TrimSpace(from[start+1 : len(from)-1])
	}
	return strings.ToLower(from)
}
