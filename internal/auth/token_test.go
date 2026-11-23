// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"testing"
	"time"
)

func TestClientCredentialExpiresAndDoesNotAuthorizeAnotherKey(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	dir := NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	dir.now = func() time.Time { return now }
	id, secret, err := dir.Create("ada", false)
	if err != nil || id == "" || secret == "" {
		t.Fatalf("create %s %s %v", id, secret, err)
	}
	token, expires, err := dir.Exchange(id, secret)
	if err != nil || expires != 600 {
		t.Fatalf("exchange %s %d %v", token, expires, err)
	}
	subject, operator, err := dir.Verify(token)
	if err != nil || subject != "ada" || operator {
		t.Fatalf("verify %s %v %v", subject, operator, err)
	}
	if _, _, err := dir.Exchange(id, "wrong"); !errors.Is(err, ErrRejected) {
		t.Fatalf("wrong secret %v", err)
	}
	other := NewDirectory([]byte("other-key"), "http://127.0.0.1:8787")
	if _, _, err := other.Verify(token); !errors.Is(err, ErrRejected) {
		t.Fatalf("other key %v", err)
	}
	dir.now = func() time.Time { return now.Add(11 * time.Minute) }
	if _, _, err := dir.Verify(token); !errors.Is(err, ErrRejected) {
		t.Fatalf("expired %v", err)
	}
}

func TestSuspendedAgentCannotExchangeOrUseAToken(t *testing.T) {
	dir := NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	id, secret, err := dir.Create("ada", false)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := dir.Exchange(id, secret)
	if err != nil {
		t.Fatal(err)
	}
	dir.Suspend("ada")
	if _, _, err := dir.Verify(token); !errors.Is(err, ErrRejected) {
		t.Fatalf("verify %v", err)
	}
	if _, _, err := dir.Exchange(id, secret); !errors.Is(err, ErrRejected) {
		t.Fatalf("exchange %v", err)
	}
}
