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

	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/store"
)

// Permission is one action a grant allows on one inbox.
type Permission string

const (
	PermRead      Permission = "read"
	PermSend      Permission = "send"
	PermReply     Permission = "reply"
	PermDelete    Permission = "delete"
	PermManage    Permission = "manage"
	PermProvision Permission = "inbox:provision"

	// provisionScope is the inbox id for a grant that creates inboxes.
	provisionScope = "*"
)

// ErrForbidden is returned when the caller has no grant for the action.
var ErrForbidden = errors.New("forbidden")

// ErrQuota is returned when a send quota or recipient-domain rate limit is exceeded.
var ErrQuota = errors.New("send quota exceeded")

// ErrSuppressed is returned when the recipient is on the hard-bounce list.
var ErrSuppressed = errors.New("recipient is suppressed")

const (
	// DefaultInboxDaily is the default daily send cap per inbox.
	DefaultInboxDaily = 50
	// DefaultDomainDaily is the default daily send cap per sender domain.
	DefaultDomainDaily = 500
	// DefaultWorkspaceDaily is the default daily send cap across the workspace.
	DefaultWorkspaceDaily = 5000
	// DefaultRecipientPerMinute is the default per-minute send rate limit per recipient domain.
	DefaultRecipientPerMinute = 30
	// DefaultStorageQuotaBytes is the default per-inbox disk storage cap (100 MiB).
	DefaultStorageQuotaBytes int64 = 100 * 1024 * 1024
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
	mail               mailbackend.Backend
	grants             []grant
	inboxDaily         int
	domainDaily        int
	workspaceDaily     int
	recipientPerMinute int
	storageQuota       int64
	now                func() time.Time
	syncLimit          int

	mu        sync.Mutex
	sent      map[string]int
	sentID    map[string]struct{}
	createdID map[string]string
	jobs      map[string]*Job
	generated map[string]GenerateResult
	keep      map[string]int
	allow     map[string][]string
	feed      *events.Feed
	db        *store.Postgres
	audit     []store.AuditRow
	nextAudit int64
}

// New returns a service over a mail backend.
// Product default send limits are 50/inbox/day, 500/domain/day, 5000/workspace/day,
// and 30/recipient-domain/minute.
func New(mail mailbackend.Backend) *Service {
	return &Service{
		mail:               mail,
		inboxDaily:         DefaultInboxDaily,
		domainDaily:        DefaultDomainDaily,
		workspaceDaily:     DefaultWorkspaceDaily,
		recipientPerMinute: DefaultRecipientPerMinute,
		storageQuota:       DefaultStorageQuotaBytes,
		now:                time.Now,
		sent:               map[string]int{},
		sentID:             map[string]struct{}{},
		createdID:          map[string]string{},
	}
}

// SetQuota replaces the daily inbox and domain send limits. Zero rejects every send.
func (s *Service) SetQuota(inboxDaily, domainDaily int) {
	s.inboxDaily = inboxDaily
	s.domainDaily = domainDaily
}

// SetSendLimits replaces all send quotas and the per-recipient-domain per-minute rate limit.
// Zero on any limit rejects every send.
func (s *Service) SetSendLimits(inboxDaily, domainDaily, workspaceDaily, recipientPerMinute int) {
	s.inboxDaily = inboxDaily
	s.domainDaily = domainDaily
	s.workspaceDaily = workspaceDaily
	s.recipientPerMinute = recipientPerMinute
}

// Identities creates and suspends agent credentials. The token still only names the caller.
type Identities interface {
	Create(agent string, operator bool) (id, secret string, err error)
	Suspend(agent string)
}

// SetDB stores grants, quotas, idempotency keys, and jobs in PostgreSQL.
// A second process that opens the same database sees them.
func (s *Service) SetDB(db *store.Postgres) {
	s.db = db
}

// Allow records one grant.
func (s *Service) Allow(agent, inbox string, perm Permission) {
	if s.db != nil {
		_ = s.db.SaveGrant(context.Background(), agent, inbox, string(perm))
		return
	}
	s.mu.Lock()
	s.grants = append(s.grants, grant{agent: agent, inbox: inbox, perm: perm})
	s.mu.Unlock()
}

// Grant records a permission. Only an operator may do that.
func (s *Service) Grant(caller Caller, agent, inbox string, perm Permission) error {
	if !caller.Operator {
		return ErrForbidden
	}
	if agent == "" || inbox == "" || !knownPermission(perm) {
		return fmt.Errorf("%w: grant needs an agent, an inbox, and a permission", ErrInvalid)
	}
	if err := grantScope(perm, inbox); err != nil {
		return err
	}
	if s.db != nil {
		if err := s.db.SaveGrant(context.Background(), agent, inbox, string(perm)); err != nil {
			return err
		}
	} else {
		s.Allow(agent, inbox, perm)
	}
	return s.record(context.Background(), caller, "grant", agent+" "+inbox+" "+string(perm))
}

// Revoke removes one permission. Only an operator may do that.
// A grant that is already gone is still a success.
func (s *Service) Revoke(caller Caller, agent, inbox string, perm Permission) error {
	if !caller.Operator {
		return ErrForbidden
	}
	if agent == "" || inbox == "" || !knownPermission(perm) {
		return fmt.Errorf("%w: revoke needs an agent, an inbox, and a permission", ErrInvalid)
	}
	if err := grantScope(perm, inbox); err != nil {
		return err
	}
	if s.db != nil {
		if err := s.db.DeleteGrant(context.Background(), agent, inbox, string(perm)); err != nil {
			return err
		}
		return s.record(context.Background(), caller, "revoke", agent+" "+inbox+" "+string(perm))
	}
	s.mu.Lock()
	kept := s.grants[:0]
	for _, item := range s.grants {
		if item.agent == agent && item.inbox == inbox && item.perm == perm {
			continue
		}
		kept = append(kept, item)
	}
	s.grants = kept
	s.mu.Unlock()
	return s.record(context.Background(), caller, "revoke", agent+" "+inbox+" "+string(perm))
}

// ProvisionAgent creates a client credential for an agent. Only an operator may do that.
func (s *Service) ProvisionAgent(caller Caller, identities Identities, agent string) (string, string, error) {
	if !caller.Operator {
		return "", "", ErrForbidden
	}
	if identities == nil {
		return "", "", fmt.Errorf("%w: credentials are not configured", ErrInvalid)
	}
	id, secret, err := identities.Create(agent, false)
	if err != nil {
		return "", "", err
	}
	if err := s.record(context.Background(), caller, "agent.create", agent); err != nil {
		return id, secret, err
	}
	return id, secret, nil
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
	return s.record(context.Background(), caller, "agent.suspend", agent)
}

func knownPermission(perm Permission) bool {
	switch perm {
	case PermRead, PermSend, PermReply, PermDelete, PermManage, PermProvision:
		return true
	default:
		return false
	}
}

func grantScope(perm Permission, inbox string) error {
	if perm == PermProvision && inbox != provisionScope {
		return fmt.Errorf("%w: inbox:provision is granted on *", ErrInvalid)
	}
	if perm != PermProvision && inbox == provisionScope {
		return fmt.Errorf("%w: * is only for inbox:provision", ErrInvalid)
	}
	return nil
}

func (s *Service) permitProvision(ctx context.Context, caller Caller) error {
	if caller.Operator {
		return nil
	}
	return s.permit(ctx, caller, provisionScope, PermProvision)
}

func (s *Service) record(ctx context.Context, caller Caller, action, target string) error {
	actor := caller.Agent
	if actor == "" && caller.Operator {
		actor = "operator"
	}
	if s.db != nil {
		return s.db.AppendAudit(ctx, actor, action, target)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextAudit++
	s.audit = append(s.audit, store.AuditRow{
		ID: s.nextAudit, At: s.now().UTC(), Actor: actor, Action: action, Target: target,
	})
	return nil
}

// Audit returns operator actions after the given id. Only an operator may read them.
// next is the last returned id when another page remains.
func (s *Service) Audit(ctx context.Context, caller Caller, after int64, limit int) ([]store.AuditRow, int64, error) {
	if !caller.Operator {
		return nil, 0, ErrForbidden
	}
	if after < 0 {
		return nil, 0, fmt.Errorf("%w: after must be zero or greater", ErrInvalid)
	}
	size, err := (mailbackend.Page{Limit: limit}).Bound()
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	var rows []store.AuditRow
	if s.db != nil {
		rows, err = s.db.AuditSince(ctx, after, size+1)
		if err != nil {
			return nil, 0, err
		}
	} else {
		s.mu.Lock()
		for _, row := range s.audit {
			if row.ID > after {
				rows = append(rows, row)
			}
		}
		s.mu.Unlock()
	}
	var next int64
	if len(rows) > size {
		rows = rows[:size]
		next = rows[len(rows)-1].ID
	}
	if rows == nil {
		rows = []store.AuditRow{}
	}
	return rows, next, nil
}

// CreateInbox creates an address. An operator may do that.
// An agent may do that when it holds inbox:provision on *.
// idemKey is the Idempotency-Key. A repeat of a completed key returns the same account id.
func (s *Service) CreateInbox(ctx context.Context, caller Caller, spec mailbackend.InboxSpec, idemKey string) (string, error) {
	if err := s.permitProvision(ctx, caller); err != nil {
		return "", err
	}
	if spec.QuotaBytes < 0 {
		return "", fmt.Errorf("%w: quotaBytes must be zero or greater", ErrInvalid)
	}
	if spec.QuotaBytes == 0 && s.storageQuota > 0 {
		spec.QuotaBytes = s.storageQuota
	}
	key := idempotencyKey(caller, spec.Local, idemKey)
	if key != "" {
		id, ok, err := s.created(ctx, key)
		if err != nil {
			return "", err
		}
		if ok {
			return id, nil
		}
	}
	id, err := s.mail.CreateInbox(ctx, spec)
	if err != nil {
		return "", err
	}
	if key != "" {
		if err := s.rememberCreated(ctx, key, id); err != nil {
			return "", err
		}
	}
	if err := s.record(ctx, caller, "inbox.create", id); err != nil {
		return "", err
	}
	return id, nil
}

// Deliver submits a message as the account. An operator may always send.
// An agent needs a send grant for that account.
// idemKey is the Idempotency-Key. A repeat of a completed key does not send again and does not consume quota.
func (s *Service) Deliver(ctx context.Context, caller Caller, msg mailbackend.Outbound, idemKey string) error {
	if err := s.permit(ctx, caller, msg.AccountID, PermSend); err != nil {
		return err
	}
	key := idempotencyKey(caller, msg.AccountID, idemKey)
	if key != "" {
		seen, err := s.alreadySent(ctx, key)
		if err != nil {
			return err
		}
		if seen {
			return nil
		}
	}
	return s.submit(ctx, key, msg)
}

func (s *Service) submit(ctx context.Context, key string, msg mailbackend.Outbound) error {
	if s.feed != nil {
		reason, suppressed, err := s.feed.Suppressed(ctx, msg.To)
		if err != nil {
			return err
		}
		if suppressed {
			return fmt.Errorf("%w: %s", ErrSuppressed, reason)
		}
	}
	if err := s.consume(ctx, msg); err != nil {
		return err
	}
	if err := s.mail.Deliver(ctx, msg); err != nil {
		s.refund(ctx, msg)
		return err
	}
	if key != "" {
		if err := s.remember(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// List returns messages in an inbox. An operator may always read.
// An agent needs a read grant for that account.
func (s *Service) List(ctx context.Context, caller Caller, accountID string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	if err := s.permit(ctx, caller, accountID, PermRead); err != nil {
		return nil, "", err
	}
	if _, err := page.Bound(); err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	messages, next, err := s.mail.List(ctx, accountID, page)
	if errors.Is(err, mailbackend.ErrPage) {
		return nil, "", fmt.Errorf("%w: cursor was not found", ErrInvalid)
	}
	return messages, next, err
}

// Search returns messages matching text. It uses the same read grant as List.
func (s *Service) Search(ctx context.Context, caller Caller, accountID, query string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	if err := s.permit(ctx, caller, accountID, PermRead); err != nil {
		return nil, "", err
	}
	if _, err := page.Bound(); err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	messages, next, err := s.mail.Search(ctx, accountID, query, page)
	if errors.Is(err, mailbackend.ErrPage) {
		return nil, "", fmt.Errorf("%w: cursor was not found", ErrInvalid)
	}
	return messages, next, err
}

// Get returns one message. It uses the same read grant as List.
func (s *Service) Get(ctx context.Context, caller Caller, accountID, messageID string) (mailbackend.Message, error) {
	if err := s.permit(ctx, caller, accountID, PermRead); err != nil {
		return mailbackend.Message{}, err
	}
	msg, err := s.mail.Get(ctx, accountID, messageID)
	if err != nil {
		return mailbackend.Message{}, err
	}
	if err := s.applyAllowlist(ctx, accountID, &msg); err != nil {
		return mailbackend.Message{}, err
	}
	return msg, nil
}

// Delete removes one message. An agent needs a delete grant for that account.
func (s *Service) Delete(ctx context.Context, caller Caller, accountID, messageID string) error {
	if err := s.permit(ctx, caller, accountID, PermDelete); err != nil {
		return err
	}
	return s.mail.Delete(ctx, accountID, messageID)
}

// ListInboxes returns every account. Only an operator may do that.
func (s *Service) ListInboxes(ctx context.Context, caller Caller, domain string, page mailbackend.Page) ([]Inbox, string, error) {
	if !caller.Operator {
		return nil, "", ErrForbidden
	}
	if _, err := page.Bound(); err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	accounts, next, err := s.mail.ListInboxes(ctx, page)
	if errors.Is(err, mailbackend.ErrPage) {
		return nil, "", fmt.Errorf("%w: cursor was not found", ErrInvalid)
	}
	if err != nil {
		return nil, "", err
	}
	out := make([]Inbox, 0, len(accounts))
	for _, account := range accounts {
		address := account.Local
		if domain != "" && !strings.Contains(account.Local, "@") {
			address = account.Local + "@" + domain
		}
		out = append(out, Inbox{ID: account.ID, Address: address})
	}
	return out, next, nil
}

// DeleteInbox removes one account. Only an operator may do that.
func (s *Service) DeleteInbox(ctx context.Context, caller Caller, id string) error {
	if !caller.Operator {
		return ErrForbidden
	}
	if id == "" {
		return fmt.Errorf("%w: inbox id is required", ErrInvalid)
	}
	if err := s.mail.DeleteInbox(ctx, id); err != nil {
		return err
	}
	return s.record(ctx, caller, "inbox.delete", id)
}

// Attachment returns the bytes of one file on a message. It uses the same read grant as Get.
func (s *Service) Attachment(ctx context.Context, caller Caller, accountID, messageID, blobID string) ([]byte, mailbackend.Attachment, error) {
	if err := s.permit(ctx, caller, accountID, PermRead); err != nil {
		return nil, mailbackend.Attachment{}, err
	}
	if blobID == "" {
		return nil, mailbackend.Attachment{}, fmt.Errorf("%w: blob id is required", ErrInvalid)
	}
	message, err := s.mail.Get(ctx, accountID, messageID)
	if err != nil {
		return nil, mailbackend.Attachment{}, err
	}
	var meta mailbackend.Attachment
	found := false
	for _, file := range message.Attachments {
		if file.BlobID == blobID {
			meta = file
			found = true
			break
		}
	}
	if !found {
		return nil, mailbackend.Attachment{}, ErrNotFound
	}
	data, err := s.mail.Download(ctx, accountID, blobID)
	if err != nil {
		return nil, mailbackend.Attachment{}, err
	}
	return data, meta, nil
}

// Reply submits a message that references an existing one.
// An agent needs a reply grant. The send quota still applies.
func (s *Service) Reply(ctx context.Context, caller Caller, accountID, messageID string, msg mailbackend.Outbound, idemKey string) error {
	if err := s.permit(ctx, caller, accountID, PermReply); err != nil {
		return err
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
	if key != "" {
		seen, err := s.alreadySent(ctx, key)
		if err != nil {
			return err
		}
		if seen {
			return nil
		}
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

func (s *Service) alreadySent(ctx context.Context, key string) (bool, error) {
	if s.db != nil {
		_, ok, err := s.db.Idempotency(ctx, "sent", key)
		return ok, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sentID[key]
	return ok, nil
}

func (s *Service) remember(ctx context.Context, key string) error {
	if s.db != nil {
		return s.db.SaveIdempotency(ctx, "sent", key, "")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sentID[key] = struct{}{}
	return nil
}

func (s *Service) created(ctx context.Context, key string) (string, bool, error) {
	if s.db != nil {
		return s.db.Idempotency(ctx, "created", key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.createdID[key]
	return id, ok, nil
}

func (s *Service) rememberCreated(ctx context.Context, key, id string) error {
	if s.db != nil {
		return s.db.SaveIdempotency(ctx, "created", key, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createdID[key] = id
	return nil
}

func (s *Service) sendBuckets(msg mailbackend.Outbound) []store.BucketLimit {
	now := s.now().UTC()
	day := now.Format("2006-01-02")
	minute := now.Format("2006-01-02T15:04")
	return []store.BucketLimit{
		{Key: "inbox\n" + msg.AccountID + "\n" + day, Limit: s.inboxDaily},
		{Key: "domain\n" + domainOf(msg.From) + "\n" + day, Limit: s.domainDaily},
		{Key: "workspace\n" + store.WorkspaceID + "\n" + day, Limit: s.workspaceDaily},
		{Key: "rcpt\n" + domainOf(msg.To) + "\n" + minute, Limit: s.recipientPerMinute},
	}
}

func (s *Service) consume(ctx context.Context, msg mailbackend.Outbound) error {
	buckets := s.sendBuckets(msg)
	if s.db != nil {
		ok, err := s.db.ConsumeBuckets(ctx, buckets)
		if err != nil {
			return err
		}
		if !ok {
			return ErrQuota
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range buckets {
		if b.Limit <= 0 || s.sent[b.Key] >= b.Limit {
			return ErrQuota
		}
	}
	for _, b := range buckets {
		s.sent[b.Key]++
	}
	return nil
}

func (s *Service) refund(ctx context.Context, msg mailbackend.Outbound) {
	buckets := s.sendBuckets(msg)
	if s.db != nil {
		keys := make([]string, len(buckets))
		for i, b := range buckets {
			keys[i] = b.Key
		}
		_ = s.db.RefundBuckets(ctx, keys)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range buckets {
		if s.sent[b.Key] > 0 {
			s.sent[b.Key]--
		}
	}
}

func domainOf(address string) string {
	_, domain, ok := strings.Cut(address, "@")
	if !ok || domain == "" {
		return "unknown"
	}
	return strings.ToLower(domain)
}

func (s *Service) permit(ctx context.Context, caller Caller, inbox string, perm Permission) error {
	ok, err := s.allowed(ctx, caller, inbox, perm)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

func (s *Service) allowed(ctx context.Context, caller Caller, inbox string, perm Permission) (bool, error) {
	if caller.Operator {
		return true, nil
	}
	if caller.Agent == "" {
		return false, nil
	}
	if s.db != nil {
		return s.db.HasGrant(ctx, caller.Agent, inbox, string(perm))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.grants {
		if item.agent == caller.Agent && item.inbox == inbox && item.perm == perm {
			return true, nil
		}
	}
	return false, nil
}
