// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
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

// ErrConflict is returned when an address is already taken, or when a delete
// names an account that is not an inbox.
var ErrConflict = errors.New("conflict")

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
	reservedLocals     map[string]bool
	reservedIDs        map[string]bool

	mu        sync.Mutex
	sent      map[string]int
	idem      map[string]idemEntry
	jobs      map[string]*Job
	generated map[string]GenerateResult
	keep      map[string]int
	allow     map[string][]string
	feed      *events.Feed
	stopped   Suspensions
	db        *store.Postgres
	audit     []store.AuditRow
	nextAudit int64
	// notices receives warnings that do not fail a request. Nil means slog.Default().
	notices *slog.Logger
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
		idem:               map[string]idemEntry{},
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

// ReserveAccounts names mail accounts that are not inboxes, such as the
// operator and service accounts inbai init creates. locals are their account
// names and ids their account ids. ListInboxes leaves them out, CreateInbox
// refuses a reserved local part, and DeleteInbox refuses a reserved id.
// Empty values are ignored. Call it before the service handles requests.
func (s *Service) ReserveAccounts(locals, ids []string) {
	s.reservedLocals = map[string]bool{}
	s.reservedIDs = map[string]bool{}
	for _, local := range locals {
		if local = strings.ToLower(strings.TrimSpace(local)); local != "" {
			s.reservedLocals[local] = true
		}
	}
	for _, id := range ids {
		if id != "" {
			s.reservedIDs[id] = true
		}
	}
}

// reserved reports whether the account is one ReserveAccounts named.
// name may be a bare local part or an address in domain.
func (s *Service) reserved(id, name, domain string) bool {
	if s.reservedIDs[id] {
		return true
	}
	name = strings.ToLower(name)
	if local, at, ok := strings.Cut(name, "@"); ok {
		if !strings.EqualFold(at, domain) {
			return false
		}
		name = local
	}
	return s.reservedLocals[name]
}

// Identities creates, suspends, and resumes agent credentials. The token still only names the caller.
// Suspend and Resume report found as false when no credential names the agent.
type Identities interface {
	Create(agent string, operator bool) (id, secret string, err error)
	Suspend(agent string) (found bool, err error)
	Resume(agent string) (found bool, err error)
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
// The audit row is written after the suspension is stored.
// An agent that no credential names is ErrNotFound.
func (s *Service) SuspendAgent(caller Caller, identities Identities, agent string) error {
	if !caller.Operator {
		return ErrForbidden
	}
	if identities == nil || agent == "" {
		return fmt.Errorf("%w: credentials are not configured", ErrInvalid)
	}
	found, err := identities.Suspend(agent)
	if err != nil {
		return fmt.Errorf("suspend %s: %w", agent, err)
	}
	if !found {
		return fmt.Errorf("%w: agent %s has no credential", ErrNotFound, agent)
	}
	return s.record(context.Background(), caller, "agent.suspend", agent)
}

// ResumeAgent lifts a suspension. Only an operator may do that.
// The audit row is written after the change is stored. An agent that is not suspended
// is still a success and still writes the row, as Revoke does for a grant that is gone.
// An agent that no credential names is ErrNotFound.
// Grants are unchanged, and a webhook the suspension disabled stays disabled.
func (s *Service) ResumeAgent(caller Caller, identities Identities, agent string) error {
	if !caller.Operator {
		return ErrForbidden
	}
	if identities == nil || agent == "" {
		return fmt.Errorf("%w: credentials are not configured", ErrInvalid)
	}
	found, err := identities.Resume(agent)
	if err != nil {
		return fmt.Errorf("resume %s: %w", agent, err)
	}
	if !found {
		return fmt.Errorf("%w: agent %s has no credential", ErrNotFound, agent)
	}
	return s.record(context.Background(), caller, "agent.resume", agent)
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
// See reserve for a repeat that is still running or names a different request.
func (s *Service) CreateInbox(ctx context.Context, caller Caller, spec mailbackend.InboxSpec, idemKey string) (string, error) {
	if err := s.permitProvision(ctx, caller); err != nil {
		return "", err
	}
	if spec.QuotaBytes < 0 {
		return "", fmt.Errorf("%w: quotaBytes must be zero or greater", ErrInvalid)
	}
	hash := createHash(spec)
	if spec.QuotaBytes == 0 && s.storageQuota > 0 {
		spec.QuotaBytes = s.storageQuota
	}
	// A reserved local part is refused before a key is reserved, like any other invalid input.
	local := strings.ToLower(strings.TrimSpace(spec.Local))
	if s.reservedLocals[local] {
		return "", fmt.Errorf("%w: %s is reserved for an inbAI account and is not an inbox", ErrConflict, local)
	}
	hold, id, done, err := s.reserve(ctx, kindCreate, idempotencyKey(caller, spec.Local, idemKey), hash)
	if err != nil {
		return "", err
	}
	if done {
		return id, nil
	}
	id, err = s.mail.CreateInbox(ctx, spec)
	if err != nil {
		s.release(ctx, hold)
		if errors.Is(err, mailbackend.ErrExists) {
			return "", fmt.Errorf("%w: an account named %s already exists", ErrConflict, local)
		}
		return "", err
	}
	if err := s.complete(ctx, hold, id); err != nil {
		return "", err
	}
	if err := s.record(ctx, caller, "inbox.create", id); err != nil {
		return "", err
	}
	return id, nil
}

// Deliver submits a message as the account. An operator may always send.
// An agent needs a send grant for that account.
// The sender is the account's own address or another address the mail server lets it send as.
// See sender. That rule applies to an operator too.
// idemKey is the Idempotency-Key. A repeat of a completed key does not send again and does not consume quota.
// See reserve for a repeat that is still running or names a different request.
func (s *Service) Deliver(ctx context.Context, caller Caller, msg mailbackend.Outbound, idemKey string) error {
	if err := s.permit(ctx, caller, msg.AccountID, PermSend); err != nil {
		return err
	}
	from, err := s.sender(ctx, msg.AccountID, msg.From)
	if err != nil {
		return err
	}
	msg.From = from
	return s.submitOnce(ctx, kindSend, idempotencyKey(caller, msg.AccountID, idemKey), sendHash("send", "", msg), msg)
}

// submitOnce submits msg unless the key already finished. The key is held while the message is
// submitted, released when the submit fails, and marked done when it succeeds.
func (s *Service) submitOnce(ctx context.Context, kind, key, hash string, msg mailbackend.Outbound) error {
	hold, _, done, err := s.reserve(ctx, kind, key, hash)
	if err != nil || done {
		return err
	}
	if err := s.submit(ctx, msg); err != nil {
		s.release(ctx, hold)
		return err
	}
	return s.complete(ctx, hold, "")
}

func (s *Service) submit(ctx context.Context, msg mailbackend.Outbound) error {
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

// ListInboxes returns every inbox. Only an operator may do that.
// Accounts named by ReserveAccounts are left out, so a page can hold fewer
// inboxes than its limit and still have a next cursor.
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
		if s.reserved(account.ID, account.Local, domain) {
			continue
		}
		address := account.Local
		if domain != "" && !strings.Contains(account.Local, "@") {
			address = account.Local + "@" + domain
		}
		out = append(out, Inbox{ID: account.ID, Address: address})
	}
	return out, next, nil
}

// DeleteInbox removes one account and what inbAI keeps for it. Only an operator may do that.
// The account is deleted first because that step cannot be undone. Removing the rest first
// would drop grants and webhooks for an inbox that still exists if the account delete failed.
// See forgetInbox for what is removed. A failure there is logged, not returned: the account is gone.
// When the account delete fails and the mail server reports that the account does not exist,
// the rest is removed too and the mail server's error is still returned, so a repeat delete
// finishes a cleanup that failed. * names no inbox: it is the scope of inbox:provision grants.
// An id that ReserveAccounts named is ErrConflict, and the backend is not called.
func (s *Service) DeleteInbox(ctx context.Context, caller Caller, id string) error {
	if !caller.Operator {
		return ErrForbidden
	}
	if id == "" {
		return fmt.Errorf("%w: inbox id is required", ErrInvalid)
	}
	if id == provisionScope {
		return fmt.Errorf("%w: * is not an inbox id", ErrInvalid)
	}
	if s.reservedIDs[id] {
		return fmt.Errorf("%w: %s is an inbAI account, not an inbox, and cannot be deleted", ErrConflict, id)
	}
	if err := s.mail.DeleteInbox(ctx, id); err != nil {
		if check, ok := s.mail.(mailbackend.InboxChecker); ok {
			if exists, checkErr := check.InboxExists(ctx, id); checkErr == nil && !exists {
				s.forgetInbox(ctx, id)
			}
		}
		return err
	}
	s.forgetInbox(ctx, id)
	return s.record(ctx, caller, "inbox.delete", id)
}

// forgetInbox removes what inbAI keeps for a deleted inbox: every agent's grants on it, its
// webhooks and their queued deliveries, its sender allowlist and retention policy, the
// idempotency keys of creates that returned its id, and its event cursor. Events and audit
// rows stay. With a database that is one transaction, store.ForgetInbox, which also covers
// the feed's rows because inbaid opens the feed on the same database.
// Each step deletes only what is there, so a repeat is safe. It runs even when the caller
// has gone away, because the account is already deleted. A failure is logged as a warning.
func (s *Service) forgetInbox(ctx context.Context, id string) {
	ctx = context.WithoutCancel(ctx)
	var err error
	if s.db != nil {
		err = s.db.ForgetInbox(ctx, store.InboxKeys{Inbox: id, RetentionScope: retentionScope(id), CreateKind: kindCreate})
	} else {
		s.mu.Lock()
		kept := s.grants[:0]
		for _, item := range s.grants {
			if item.inbox != id {
				kept = append(kept, item)
			}
		}
		s.grants = kept
		delete(s.allow, id)
		delete(s.keep, retentionScope(id))
		for key, entry := range s.idem {
			if strings.HasPrefix(key, kindCreate+"\n") && entry.done && entry.payload == id {
				delete(s.idem, key)
			}
		}
		s.mu.Unlock()
		if s.feed != nil {
			err = s.forgetFeed(ctx, id)
		}
	}
	if err != nil {
		s.logger().Warn("inbox deleted, but removing its grants, webhooks, or settings failed; delete the inbox again to finish",
			"inbox", id, "error", err.Error())
	}
}

// forgetFeed removes the inbox's webhooks, their deliveries, and its cursor from a feed
// that has no database behind the service.
func (s *Service) forgetFeed(ctx context.Context, id string) error {
	subs, err := s.feed.Subscriptions(ctx, id)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		if err := s.feed.Unsubscribe(ctx, sub.ID); err != nil {
			return err
		}
	}
	return s.feed.Forget(ctx, id)
}

func (s *Service) logger() *slog.Logger {
	if s.notices != nil {
		return s.notices
	}
	return slog.Default()
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
// The reply goes to the original sender. See replyRecipient. The sender follows the same rule as Deliver.
// idemKey is the Idempotency-Key, as for Deliver. Replies have their own keys: a key used for a send does not answer a reply.
func (s *Service) Reply(ctx context.Context, caller Caller, accountID, messageID string, msg mailbackend.Outbound, idemKey string) error {
	if err := s.permit(ctx, caller, accountID, PermReply); err != nil {
		return err
	}
	original, err := s.mail.Get(ctx, accountID, messageID)
	if err != nil {
		return err
	}
	to, err := s.replyRecipient(ctx, caller, accountID, original, msg.To)
	if err != nil {
		return err
	}
	from, err := s.sender(ctx, accountID, msg.From)
	if err != nil {
		return err
	}
	msg.From = from
	msg.To = to
	msg.AccountID = accountID
	if msg.Subject == "" {
		msg.Subject = "Re: " + original.Subject
	}
	msg.InReplyTo = original.MessageID
	msg.References = append(append([]string{}, original.References...), original.MessageID...)
	return s.submitOnce(ctx, kindReply, idempotencyKey(caller, accountID, idemKey), sendHash("reply", messageID, msg), msg)
}

// sender returns the address a message from the account is sent as.
// An empty from is the account's own address, the first one the backend lists.
// Any other from must be an address the backend lets the account send as, compared case-insensitively.
// Anything else is ErrForbidden for every caller: the mail server would refuse it too.
// A failed lookup is returned as is, and an empty one is ErrForbidden, so nothing is sent unchecked.
func (s *Service) sender(ctx context.Context, accountID, from string) (string, error) {
	addresses, err := s.mail.SenderAddresses(ctx, accountID)
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("%w: inbox %s has no address it may send as", ErrForbidden, accountID)
	}
	if strings.TrimSpace(from) == "" {
		return addresses[0], nil
	}
	want := firstAddress(from)
	for _, address := range addresses {
		if want != "" && strings.EqualFold(address, want) {
			return address, nil
		}
	}
	return "", fmt.Errorf("%w: from %q is not an address this inbox may send as; omit from to send as the inbox", ErrForbidden, from)
}

// replyRecipient returns where a reply to original goes: its first Reply-To address, else its From.
// An empty to, or one equal to that address, needs only the reply grant the caller already passed.
// A different to also needs send on the inbox.
func (s *Service) replyRecipient(ctx context.Context, caller Caller, accountID string, original mailbackend.Message, to string) (string, error) {
	recipient := firstAddress(original.ReplyTo)
	if recipient == "" {
		recipient = firstAddress(original.From)
	}
	if recipient == "" {
		return "", fmt.Errorf("%w: the message has no From or Reply-To address to reply to", ErrInvalid)
	}
	if strings.TrimSpace(to) == "" || strings.EqualFold(firstAddress(to), recipient) {
		return recipient, nil
	}
	ok, err := s.allowed(ctx, caller, accountID, PermSend)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: a reply goes to the original sender; replying to another address needs send on this inbox", ErrForbidden)
	}
	return to, nil
}

// firstAddress returns the first address in an address header value such as "Ada <ada@example.com>, bob@example.com".
// It returns "" when the value holds no address.
func firstAddress(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	if list, err := mail.ParseAddressList(header); err == nil && len(list) > 0 {
		return list[0].Address
	}
	address := strings.TrimSpace(firstListItem(header))
	if one, err := mail.ParseAddress(address); err == nil {
		return one.Address
	}
	if start := strings.LastIndex(address, "<"); start >= 0 && strings.HasSuffix(address, ">") {
		address = strings.TrimSpace(address[start+1 : len(address)-1])
	}
	if !strings.Contains(address, "@") || strings.ContainsAny(address, " \t\r\n<>,\"") {
		return ""
	}
	return address
}

// firstListItem returns the text before the first comma that is not inside quotes or angle brackets.
func firstListItem(header string) string {
	quoted, angle := false, false
	for i, r := range header {
		switch {
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '<':
			angle = true
		case r == '>':
			angle = false
		case r == ',' && !angle:
			return header[:i]
		}
	}
	return header
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
