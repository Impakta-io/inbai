// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

// Permission is one action a grant allows on one inbox.
type Permission string

const (
	PermRead Permission = "read"
	PermSend Permission = "send"
)

// ErrForbidden is returned when the caller has no grant for the action.
var ErrForbidden = fmt.Errorf("forbidden")

// Caller is who is asking. An operator is the human running the CLI.
// An agent is named and may act only where a grant allows it.
type Caller struct {
	Operator bool
	Agent    string
}

type grant struct {
	agent string
	inbox string
	perm  Permission
}

// Service is the isolation boundary. Transports call this and do not check grants themselves.
type Service struct {
	mail   mailbackend.Backend
	grants []grant
}

// New returns a service over a mail backend.
func New(mail mailbackend.Backend) *Service {
	return &Service{mail: mail}
}

// Allow records one grant. The store will own this once PostgreSQL is wired.
func (s *Service) Allow(agent, inbox string, perm Permission) {
	s.grants = append(s.grants, grant{agent: agent, inbox: inbox, perm: perm})
}

// CreateInbox creates an address. Only an operator may do that.
func (s *Service) CreateInbox(ctx context.Context, caller Caller, spec mailbackend.InboxSpec) (string, error) {
	if !caller.Operator {
		return "", ErrForbidden
	}
	return s.mail.CreateInbox(ctx, spec)
}

// Deliver submits a message as the account. An operator may always send.
// An agent needs a send grant for that account.
func (s *Service) Deliver(ctx context.Context, caller Caller, msg mailbackend.Outbound) error {
	if !s.allowed(caller, msg.AccountID, PermSend) {
		return ErrForbidden
	}
	return s.mail.Deliver(ctx, msg)
}

// List returns messages in an inbox. An operator may always read.
// An agent needs a read grant for that account.
func (s *Service) List(ctx context.Context, caller Caller, accountID string) ([]mailbackend.Message, error) {
	if !s.allowed(caller, accountID, PermRead) {
		return nil, ErrForbidden
	}
	return s.mail.List(ctx, accountID)
}

func (s *Service) allowed(caller Caller, inbox string, perm Permission) bool {
	if caller.Operator {
		return true
	}
	if caller.Agent == "" {
		return false
	}
	for _, item := range s.grants {
		if item.agent == caller.Agent && item.inbox == inbox && item.perm == perm {
			return true
		}
	}
	return false
}
