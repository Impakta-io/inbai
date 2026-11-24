// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/store"
)

// ErrInProgress is returned when another request with the same Idempotency-Key has not finished.
var ErrInProgress = errors.New("a request with this Idempotency-Key is still in progress; retry later")

// ErrKeyReused is returned when an Idempotency-Key is repeated with a different request.
var ErrKeyReused = errors.New("this Idempotency-Key was used for a different request")

// Idempotency namespaces. Each operation has its own, so a key used for a send does not
// answer a reply. "sent" and "created" are the names rows were stored under before replies
// had their own namespace, so keys completed before an upgrade still match.
const (
	kindSend   = "sent"
	kindReply  = "reply"
	kindCreate = "created"
)

// idempotencyTakeover is how old an in-progress key must be before another request may take it over.
// A request whose process stopped mid-send never finishes its key. The listener gives a response
// five minutes (WriteTimeout), so no live request holds a key that long. Twice that leaves room
// for a slow backend call still running after the response deadline.
const idempotencyTakeover = 10 * time.Minute

// idemEntry is one key held in memory when no database is configured.
type idemEntry struct {
	done    bool
	hash    string
	payload string
	at      time.Time
}

// claim is a key this request reserved. The zero claim means the request has no key.
type claim struct {
	kind, key string
	at        time.Time
}

// reserve holds key for this request before the work is done.
// done is true when the same request already finished, with its payload.
// The same request still in progress is ErrInProgress. A different request is ErrKeyReused.
// An in-progress key older than idempotencyTakeover is taken over.
// A key stored before requests were hashed has no hash and matches any request.
func (s *Service) reserve(ctx context.Context, kind, key, hash string) (c claim, payload string, done bool, err error) {
	if key == "" {
		return claim{}, "", false, nil
	}
	at := s.now().UTC().Truncate(time.Microsecond)
	stale := at.Add(-idempotencyTakeover)
	if s.db != nil {
		reserved, found, err := s.db.ReserveIdempotency(ctx, kind, key, hash, at, stale)
		if err != nil {
			return claim{}, "", false, err
		}
		if reserved {
			return claim{kind: kind, key: key, at: at}, "", false, nil
		}
		return claim{}, found.Payload, true, held(found.State == store.IdempotencyDone, found.RequestHash, hash)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := kind + "\n" + key
	entry, ok := s.idem[id]
	if !ok || (!entry.done && entry.hash == hash && entry.at.Before(stale)) {
		s.idem[id] = idemEntry{hash: hash, at: at}
		return claim{kind: kind, key: key, at: at}, "", false, nil
	}
	return claim{}, entry.payload, true, held(entry.done, entry.hash, hash)
}

// held is the answer for a key another request holds. nil means that request finished and matches.
func held(done bool, stored, hash string) error {
	if stored != "" && stored != hash {
		return ErrKeyReused
	}
	if !done {
		return ErrInProgress
	}
	return nil
}

// complete marks the claim done with its payload. It runs even when the caller has gone away,
// because the work it records has already happened.
func (s *Service) complete(ctx context.Context, c claim, payload string) error {
	if c.key == "" {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	if s.db != nil {
		return s.db.CompleteIdempotency(ctx, c.kind, c.key, c.at, payload)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := c.kind + "\n" + c.key
	if entry, ok := s.idem[id]; ok && !entry.done && entry.at.Equal(c.at) {
		s.idem[id] = idemEntry{done: true, hash: entry.hash, payload: payload, at: c.at}
	}
	return nil
}

// release drops the claim after the work failed, so a retry with the same key can run.
// A failed release leaves the key to be taken over after idempotencyTakeover.
func (s *Service) release(ctx context.Context, c claim) {
	if c.key == "" {
		return
	}
	ctx = context.WithoutCancel(ctx)
	if s.db != nil {
		_ = s.db.ReleaseIdempotency(ctx, c.kind, c.key, c.at)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := c.kind + "\n" + c.key
	if entry, ok := s.idem[id]; ok && !entry.done && entry.at.Equal(c.at) {
		delete(s.idem, id)
	}
}

// sendRequest is the part of a send or reply an Idempotency-Key is bound to.
// MessageID is empty for a send. Attachments are named by the SHA-256 of their bytes.
type sendRequest struct {
	Op          string        `json:"op"`
	Inbox       string        `json:"inbox"`
	MessageID   string        `json:"messageId,omitempty"`
	From        string        `json:"from"`
	To          string        `json:"to"`
	Subject     string        `json:"subject"`
	Text        string        `json:"text"`
	Attachments []fileRequest `json:"attachments"`
}

type fileRequest struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	SHA256 string `json:"sha256"`
}

// sendHash is the request hash for a send (messageID empty) or a reply.
// It hashes the message as it will be submitted, after from, to, and subject are resolved.
func sendHash(op, messageID string, msg mailbackend.Outbound) string {
	req := sendRequest{
		Op: op, Inbox: msg.AccountID, MessageID: messageID,
		From: msg.From, To: msg.To, Subject: msg.Subject, Text: msg.Body,
		Attachments: make([]fileRequest, 0, len(msg.Attachments)),
	}
	for _, file := range msg.Attachments {
		sum := sha256.Sum256(file.Data)
		req.Attachments = append(req.Attachments, fileRequest{Name: file.Name, Type: file.Type, SHA256: hex.EncodeToString(sum[:])})
	}
	return hashJSON(req)
}

// inboxPlacer is a mail backend that creates every inbox in one fixed place
// on its mail server and names that place with two opaque strings.
type inboxPlacer interface {
	InboxPlacement() (string, string)
}

// createHash is the request hash for an inbox create, taken before the default quota is applied.
// Each InboxSpec once carried the backend's domain and role ids, and stored hashes include them
// under the JSON names domainId and roleId. The backend supplies them now, through inboxPlacer,
// so a create repeated with a key stored before then still matches. Another backend hashes them
// as empty strings.
func createHash(backend mailbackend.Backend, spec mailbackend.InboxSpec) string {
	var first, second string
	if placer, ok := backend.(inboxPlacer); ok {
		first, second = placer.InboxPlacement()
	}
	return hashJSON(struct {
		Op         string `json:"op"`
		First      string `json:"domainId"`
		Second     string `json:"roleId"`
		Local      string `json:"local"`
		QuotaBytes int64  `json:"quotaBytes"`
	}{"create", first, second, spec.Local, spec.QuotaBytes})
}

func hashJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err) // the values are strings and numbers, which always marshal
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
