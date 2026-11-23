// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

// Permission is one action a grant allows on one inbox.
type Permission string

const (
	PermRead   Permission = "read"
	PermSend   Permission = "send"
	PermReply  Permission = "reply"
	PermDelete Permission = "delete"
	PermManage Permission = "manage"
)

// ErrForbidden is returned when the caller has no grant for the action.
var ErrForbidden = errors.New("forbidden")

// ErrQuota is returned when the inbox or the domain has used its daily send quota.
var ErrQuota = errors.New("send quota exceeded")

const (
	defaultInboxDaily  = 50
	defaultDomainDaily = 500
)

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
	mail        mailbackend.Backend
	grants      []grant
	inboxDaily  int
	domainDaily int
	now         func() time.Time
	syncLimit   int

	mu        sync.Mutex
	sent      map[string]int
	sentID    map[string]struct{}
	createdID map[string]string
	jobs      map[string]*Job
	generated map[string]GenerateResult
}

// New returns a service over a mail backend.
// The default send quota is 50 messages per inbox per day and 500 per domain per day.
func New(mail mailbackend.Backend) *Service {
	return &Service{
		mail:        mail,
		inboxDaily:  defaultInboxDaily,
		domainDaily: defaultDomainDaily,
		now:         time.Now,
		sent:        map[string]int{},
		sentID:      map[string]struct{}{},
		createdID:   map[string]string{},
	}
}

// SetQuota replaces the daily send limits. Zero rejects every send.
func (s *Service) SetQuota(inboxDaily, domainDaily int) {
	s.inboxDaily = inboxDaily
	s.domainDaily = domainDaily
}

// Identities creates and suspends agent credentials. The token still only names the caller.
type Identities interface {
	Create(agent string, operator bool) (id, secret string, err error)
	Suspend(agent string)
}

// Allow records one grant. The store will own this once PostgreSQL is wired.
func (s *Service) Allow(agent, inbox string, perm Permission) {
	s.grants = append(s.grants, grant{agent: agent, inbox: inbox, perm: perm})
}

// Grant records a permission. Only an operator may do that.
func (s *Service) Grant(caller Caller, agent, inbox string, perm Permission) error {
	if !caller.Operator {
		return ErrForbidden
	}
	if agent == "" || inbox == "" || !knownPermission(perm) {
		return fmt.Errorf("%w: grant needs an agent, an inbox, and a permission", ErrInvalid)
	}
	s.Allow(agent, inbox, perm)
	return nil
}

// ProvisionAgent creates a client credential for an agent. Only an operator may do that.
func (s *Service) ProvisionAgent(caller Caller, identities Identities, agent string) (string, string, error) {
	if !caller.Operator {
		return "", "", ErrForbidden
	}
	if identities == nil {
		return "", "", fmt.Errorf("%w: credentials are not configured", ErrInvalid)
	}
	return identities.Create(agent, false)
}

// SuspendAgent stops an agent. Only an operator may do that.
func (s *Service) SuspendAgent(caller Caller, identities Identities, agent string) error {
	if !caller.Operator {
		return ErrForbidden
	}
	if identities == nil || agent == "" {
		return fmt.Errorf("%w: credentials are not configured", ErrInvalid)
	}
	identities.Suspend(agent)
	return nil
}

func knownPermission(perm Permission) bool {
	switch perm {
	case PermRead, PermSend, PermReply, PermDelete, PermManage:
		return true
	default:
		return false
	}
}

// CreateInbox creates an address. Only an operator may do that.
// idemKey is the Idempotency-Key. A repeat of a completed key returns the same account id.
func (s *Service) CreateInbox(ctx context.Context, caller Caller, spec mailbackend.InboxSpec, idemKey string) (string, error) {
	if !caller.Operator {
		return "", ErrForbidden
	}
	key := idempotencyKey(caller, spec.Local, idemKey)
	if key != "" {
		if id, ok := s.created(key); ok {
			return id, nil
		}
	}
	id, err := s.mail.CreateInbox(ctx, spec)
	if err != nil {
		return "", err
	}
	if key != "" {
		s.rememberCreated(key, id)
	}
	return id, nil
}

// Deliver submits a message as the account. An operator may always send.
// An agent needs a send grant for that account.
// idemKey is the Idempotency-Key. A repeat of a completed key does not send again and does not consume quota.
func (s *Service) Deliver(ctx context.Context, caller Caller, msg mailbackend.Outbound, idemKey string) error {
	if !s.allowed(caller, msg.AccountID, PermSend) {
		return ErrForbidden
	}
	key := idempotencyKey(caller, msg.AccountID, idemKey)
	if key != "" && s.alreadySent(key) {
		return nil
	}
	return s.submit(ctx, key, msg)
}

func (s *Service) submit(ctx context.Context, key string, msg mailbackend.Outbound) error {
	if err := s.consume(msg); err != nil {
		return err
	}
	if err := s.mail.Deliver(ctx, msg); err != nil {
		s.refund(msg)
		return err
	}
	if key != "" {
		s.remember(key)
	}
	return nil
}

// List returns messages in an inbox. An operator may always read.
// An agent needs a read grant for that account.
func (s *Service) List(ctx context.Context, caller Caller, accountID string) ([]mailbackend.Message, error) {
	if !s.allowed(caller, accountID, PermRead) {
		return nil, ErrForbidden
	}
	return s.mail.List(ctx, accountID)
}

// Search returns messages matching text. It uses the same read grant as List.
func (s *Service) Search(ctx context.Context, caller Caller, accountID, query string) ([]mailbackend.Message, error) {
	if !s.allowed(caller, accountID, PermRead) {
		return nil, ErrForbidden
	}
	return s.mail.Search(ctx, accountID, query)
}

// Get returns one message. It uses the same read grant as List.
func (s *Service) Get(ctx context.Context, caller Caller, accountID, messageID string) (mailbackend.Message, error) {
	if !s.allowed(caller, accountID, PermRead) {
		return mailbackend.Message{}, ErrForbidden
	}
	return s.mail.Get(ctx, accountID, messageID)
}

// Delete removes one message. An agent needs a delete grant for that account.
func (s *Service) Delete(ctx context.Context, caller Caller, accountID, messageID string) error {
	if !s.allowed(caller, accountID, PermDelete) {
		return ErrForbidden
	}
	return s.mail.Delete(ctx, accountID, messageID)
}

// Reply submits a message that references an existing one.
// An agent needs a reply grant. The send quota still applies.
func (s *Service) Reply(ctx context.Context, caller Caller, accountID, messageID string, msg mailbackend.Outbound, idemKey string) error {
	if !s.allowed(caller, accountID, PermReply) {
		return ErrForbidden
	}
	original, err := s.mail.Get(ctx, accountID, messageID)
	if err != nil {
		return err
	}
	msg.AccountID = accountID
	if msg.Subject == "" {
		msg.Subject = "Re: " + original.Subject
	}
	msg.InReplyTo = original.MessageID
	msg.References = append(append([]string{}, original.References...), original.MessageID...)
	key := idempotencyKey(caller, accountID, idemKey)
	if key != "" && s.alreadySent(key) {
		return nil
	}
	return s.submit(ctx, key, msg)
}

func idempotencyKey(caller Caller, inbox, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	who := caller.Agent
	if caller.Operator {
		who = "operator"
	}
	return who + "\n" + inbox + "\n" + key
}

func (s *Service) alreadySent(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sentID[key]
	return ok
}

func (s *Service) remember(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sentID[key] = struct{}{}
}

func (s *Service) created(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.createdID[key]
	return id, ok
}

func (s *Service) rememberCreated(key string, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createdID[key] = id
}

func (s *Service) consume(msg mailbackend.Outbound) error {
	day := s.now().UTC().Format("2006-01-02")
	inboxKey := "inbox\n" + msg.AccountID + "\n" + day
	domainKey := "domain\n" + domainOf(msg.From) + "\n" + day
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent[inboxKey] >= s.inboxDaily || s.sent[domainKey] >= s.domainDaily {
		return ErrQuota
	}
	s.sent[inboxKey]++
	s.sent[domainKey]++
	return nil
}

func (s *Service) refund(msg mailbackend.Outbound) {
	day := s.now().UTC().Format("2006-01-02")
	inboxKey := "inbox\n" + msg.AccountID + "\n" + day
	domainKey := "domain\n" + domainOf(msg.From) + "\n" + day
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent[inboxKey] > 0 {
		s.sent[inboxKey]--
	}
	if s.sent[domainKey] > 0 {
		s.sent[domainKey]--
	}
}

func domainOf(address string) string {
	_, domain, ok := strings.Cut(address, "@")
	if !ok || domain == "" {
		return "unknown"
	}
	return strings.ToLower(domain)
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
