// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const tokenTTL = 10 * time.Minute

// ErrRejected is returned when a credential or token is not accepted.
var ErrRejected = errors.New("rejected")

// ErrUnavailable is returned when the credential store cannot be read.
// The credential or token is not accepted. The caller may retry.
var ErrUnavailable = errors.New("credential store is unavailable")

// Store is the PostgreSQL state a Directory keeps. *store.Postgres implements it.
type Store interface {
	SaveClient(ctx context.Context, id, agent string, hash []byte, operator bool) error
	Client(ctx context.Context, id string) (agent string, hash []byte, operator bool, ok bool, err error)
	AgentExists(ctx context.Context, agent string) (bool, error)
	SuspendAgent(ctx context.Context, agent string) error
	ResumeAgent(ctx context.Context, agent string) error
	AgentSuspended(ctx context.Context, agent string) (bool, error)
}

// Directory issues client credentials and verifies access tokens.
// A token names the caller. It does not authorize an inbox.
type Directory struct {
	key      []byte
	audience string
	now      func() time.Time

	db        Store
	mu        sync.Mutex
	clients   map[string]storedClient
	suspended map[string]bool
}

type storedClient struct {
	hash     []byte
	agent    string
	operator bool
}

type claims struct {
	Subject  string `json:"sub"`
	Audience string `json:"aud"`
	Expires  int64  `json:"exp"`
	Operator bool   `json:"op"`
}

// NewDirectory returns a directory that signs tokens with key for audience.
func NewDirectory(key []byte, audience string) *Directory {
	copied := append([]byte(nil), key...)
	return &Directory{
		key:       copied,
		audience:  audience,
		now:       time.Now,
		clients:   map[string]storedClient{},
		suspended: map[string]bool{},
	}
}

// SetDB stores client credentials and suspensions in PostgreSQL.
func (d *Directory) SetDB(db Store) {
	d.db = db
}

// Create issues a client id and a secret. The secret is returned once.
// The stored value is a hash. agent is the token subject.
func (d *Directory) Create(agent string, operator bool) (string, string, error) {
	if strings.TrimSpace(agent) == "" {
		return "", "", fmt.Errorf("%w: agent name is empty", ErrRejected)
	}
	id, err := randomToken(16)
	if err != nil {
		return "", "", err
	}
	secret, err := randomToken(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(secret))
	if d.db != nil {
		if err := d.db.SaveClient(context.Background(), id, agent, sum[:], operator); err != nil {
			return "", "", err
		}
		return id, secret, nil
	}
	d.mu.Lock()
	d.clients[id] = storedClient{hash: sum[:], agent: agent, operator: operator}
	d.mu.Unlock()
	return id, secret, nil
}

// Exchange checks a client secret and returns a signed bearer token.
func (d *Directory) Exchange(id, secret string) (string, int, error) {
	sum := sha256.Sum256([]byte(secret))
	var client storedClient
	var ok bool
	if d.db != nil {
		agent, hash, operator, found, err := d.db.Client(context.Background(), id)
		if err != nil {
			return "", 0, err
		}
		client = storedClient{hash: hash, agent: agent, operator: operator}
		ok = found
	} else {
		d.mu.Lock()
		client, ok = d.clients[id]
		d.mu.Unlock()
	}
	if !ok || !hmac.Equal(sum[:], client.hash) {
		return "", 0, ErrRejected
	}
	suspended, err := d.Suspended(client.agent)
	if err != nil {
		return "", 0, err
	}
	if suspended {
		return "", 0, ErrRejected
	}
	token, err := d.sign(claims{
		Subject:  client.agent,
		Audience: d.audience,
		Expires:  d.now().Add(tokenTTL).Unix(),
		Operator: client.operator,
	})
	if err != nil {
		return "", 0, err
	}
	return token, int(tokenTTL.Seconds()), nil
}

// Suspend stops an agent from exchanging a secret or using an existing token.
// found is false when no credential names the agent. Nothing is stored then.
// The suspension is stored when err is nil and found is true.
func (d *Directory) Suspend(agent string) (found bool, err error) {
	if d.db != nil {
		ctx := context.Background()
		found, err := d.db.AgentExists(ctx, agent)
		if err != nil || !found {
			return false, err
		}
		if err := d.db.SuspendAgent(ctx, agent); err != nil {
			return false, err
		}
		return true, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, client := range d.clients {
		if client.agent == agent {
			d.suspended[agent] = true
			return true, nil
		}
	}
	return false, nil
}

// Resume lifts a suspension, so the agent can exchange its secret again.
// An access token issued before the suspension is accepted again until it expires:
// a suspension blocks tokens, it does not revoke them.
// found is false when no credential names the agent. Nothing is changed then.
// The suspension is removed when err is nil and found is true. An agent that is not
// suspended is found and left as it is.
func (d *Directory) Resume(agent string) (found bool, err error) {
	if d.db != nil {
		ctx := context.Background()
		found, err := d.db.AgentExists(ctx, agent)
		if err != nil || !found {
			return false, err
		}
		if err := d.db.ResumeAgent(ctx, agent); err != nil {
			return false, err
		}
		return true, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, client := range d.clients {
		if client.agent == agent {
			delete(d.suspended, agent)
			return true, nil
		}
	}
	return false, nil
}

// Suspended reports whether the agent is stopped.
// err wraps ErrUnavailable when the suspension state cannot be read.
func (d *Directory) Suspended(agent string) (bool, error) {
	if d.db != nil {
		ok, err := d.db.AgentSuspended(context.Background(), agent)
		if err != nil {
			return false, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return ok, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.suspended[agent], nil
}

// Verify checks the signature, audience, expiry, and suspension of an access token.
// A token is not accepted when its suspension state cannot be read. err wraps ErrUnavailable then.
func (d *Directory) Verify(token string) (subject string, operator bool, err error) {
	body, err := d.parse(token)
	if err != nil {
		return "", false, err
	}
	suspended, err := d.Suspended(body.Subject)
	if err != nil {
		return "", false, err
	}
	if suspended {
		return "", false, ErrRejected
	}
	return body.Subject, body.Operator, nil
}

func (d *Directory) sign(body claims) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, d.key)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (d *Directory) parse(token string) (claims, error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || payload == "" || sig == "" {
		return claims{}, ErrRejected
	}
	mac := hmac.New(sha256.New, d.key)
	_, _ = mac.Write([]byte(payload))
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, mac.Sum(nil)) {
		return claims{}, ErrRejected
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return claims{}, ErrRejected
	}
	var body claims
	if err := json.Unmarshal(raw, &body); err != nil {
		return claims{}, ErrRejected
	}
	if body.Audience != d.audience || body.Subject == "" || body.Expires <= d.now().Unix() {
		return claims{}, ErrRejected
	}
	return body, nil
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
