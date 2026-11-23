// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package auth

import (
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

// Directory issues client credentials and verifies access tokens.
// A token names the caller. It does not authorize an inbox.
type Directory struct {
	key      []byte
	audience string
	now      func() time.Time

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
	d.mu.Lock()
	d.clients[id] = storedClient{hash: sum[:], agent: agent, operator: operator}
	d.mu.Unlock()
	return id, secret, nil
}

// Exchange checks a client secret and returns a signed bearer token.
func (d *Directory) Exchange(id, secret string) (string, int, error) {
	sum := sha256.Sum256([]byte(secret))
	d.mu.Lock()
	client, ok := d.clients[id]
	suspended := d.suspended[client.agent]
	d.mu.Unlock()
	if !ok || !hmac.Equal(sum[:], client.hash) || suspended {
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
func (d *Directory) Suspend(agent string) {
	d.mu.Lock()
	d.suspended[agent] = true
	d.mu.Unlock()
}

// Suspended reports whether the agent is stopped.
func (d *Directory) Suspended(agent string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.suspended[agent]
}

// Verify checks the signature, audience, expiry, and suspension of an access token.
func (d *Directory) Verify(token string) (subject string, operator bool, err error) {
	body, err := d.parse(token)
	if err != nil {
		return "", false, err
	}
	if d.Suspended(body.Subject) {
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
