// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"sync"
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
	if found, err := dir.Suspend("ada"); err != nil || !found {
		t.Fatalf("suspend found %v err %v", found, err)
	}
	if _, _, err := dir.Verify(token); !errors.Is(err, ErrRejected) {
		t.Fatalf("verify %v", err)
	}
	if _, _, err := dir.Exchange(id, secret); !errors.Is(err, ErrRejected) {
		t.Fatalf("exchange %v", err)
	}
}

func TestSuspendUnknownAgentStoresNothing(t *testing.T) {
	dir := NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	if found, err := dir.Suspend("nobody"); err != nil || found {
		t.Fatalf("memory suspend found %v err %v", found, err)
	}
	db := newFakeStore()
	dir.SetDB(db)
	if found, err := dir.Suspend("nobody"); err != nil || found {
		t.Fatalf("db suspend found %v err %v", found, err)
	}
	if len(db.suspended) != 0 {
		t.Fatalf("stored %v", db.suspended)
	}
}

func TestSuspendReturnsTheStoreError(t *testing.T) {
	db := newFakeStore()
	dir := NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	dir.SetDB(db)
	id, secret, err := dir.Create("ada", false)
	if err != nil {
		t.Fatal(err)
	}
	db.suspendErr = errors.New("connection refused")
	if _, err := dir.Suspend("ada"); !errors.Is(err, db.suspendErr) {
		t.Fatalf("suspend err %v", err)
	}
	db.suspendErr = nil
	db.existsErr = errors.New("connection refused")
	if _, err := dir.Suspend("ada"); !errors.Is(err, db.existsErr) {
		t.Fatalf("lookup err %v", err)
	}
	db.existsErr = nil
	if _, _, err := dir.Exchange(id, secret); err != nil {
		t.Fatalf("a failed suspension stopped the agent: %v", err)
	}
}

func TestResumeLetsTheAgentExchangeAndUseItsTokenAgain(t *testing.T) {
	for name, db := range map[string]*fakeStore{"memory": nil, "db": newFakeStore()} {
		t.Run(name, func(t *testing.T) {
			dir := NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
			if db != nil {
				dir.SetDB(db)
			}
			id, secret, err := dir.Create("ada", false)
			if err != nil {
				t.Fatal(err)
			}
			token, _, err := dir.Exchange(id, secret)
			if err != nil {
				t.Fatal(err)
			}
			if found, err := dir.Suspend("ada"); err != nil || !found {
				t.Fatalf("suspend found %v err %v", found, err)
			}
			if _, _, err := dir.Verify(token); !errors.Is(err, ErrRejected) {
				t.Fatalf("suspended verify %v", err)
			}
			if found, err := dir.Resume("ada"); err != nil || !found {
				t.Fatalf("resume found %v err %v", found, err)
			}
			if subject, _, err := dir.Verify(token); err != nil || subject != "ada" {
				t.Fatalf("token issued before the suspension: subject %q err %v", subject, err)
			}
			if _, _, err := dir.Exchange(id, secret); err != nil {
				t.Fatalf("exchange after resume %v", err)
			}
			if found, err := dir.Resume("ada"); err != nil || !found {
				t.Fatalf("resume when not suspended found %v err %v", found, err)
			}
		})
	}
}

func TestResumeUnknownAgentIsNotFound(t *testing.T) {
	dir := NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	if found, err := dir.Resume("nobody"); err != nil || found {
		t.Fatalf("memory resume found %v err %v", found, err)
	}
	dir.SetDB(newFakeStore())
	if found, err := dir.Resume("nobody"); err != nil || found {
		t.Fatalf("db resume found %v err %v", found, err)
	}
}

func TestResumeReturnsTheStoreErrorAndTheAgentStaysSuspended(t *testing.T) {
	db := newFakeStore()
	dir := NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	dir.SetDB(db)
	id, secret, err := dir.Create("ada", false)
	if err != nil {
		t.Fatal(err)
	}
	if found, err := dir.Suspend("ada"); err != nil || !found {
		t.Fatalf("suspend found %v err %v", found, err)
	}
	db.resumeErr = errors.New("connection refused")
	if _, err := dir.Resume("ada"); !errors.Is(err, db.resumeErr) {
		t.Fatalf("resume err %v", err)
	}
	db.resumeErr = nil
	db.existsErr = errors.New("connection refused")
	if _, err := dir.Resume("ada"); !errors.Is(err, db.existsErr) {
		t.Fatalf("lookup err %v", err)
	}
	db.existsErr = nil
	if _, _, err := dir.Exchange(id, secret); !errors.Is(err, ErrRejected) {
		t.Fatalf("a failed resume let the agent back in: %v", err)
	}
}

func TestVerifyFailsClosedWhenSuspensionCannotBeRead(t *testing.T) {
	db := newFakeStore()
	dir := NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	dir.SetDB(db)
	id, secret, err := dir.Create("ada", false)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := dir.Exchange(id, secret)
	if err != nil {
		t.Fatal(err)
	}
	if found, err := dir.Suspend("ada"); err != nil || !found {
		t.Fatalf("suspend found %v err %v", found, err)
	}
	if _, _, err := dir.Verify(token); !errors.Is(err, ErrRejected) {
		t.Fatalf("suspended verify %v", err)
	}
	db.readErr = errors.New("connection refused")
	subject, _, err := dir.Verify(token)
	if !errors.Is(err, ErrUnavailable) || subject != "" {
		t.Fatalf("verify during a database error: subject %q err %v", subject, err)
	}
	if _, _, err := dir.Exchange(id, secret); err == nil {
		t.Fatal("exchange during a database error issued a token")
	}
}

// fakeStore keeps Directory state in memory and fails on demand.
type fakeStore struct {
	mu         sync.Mutex
	clients    map[string]storedClient
	suspended  map[string]bool
	existsErr  error
	suspendErr error
	resumeErr  error
	readErr    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{clients: map[string]storedClient{}, suspended: map[string]bool{}}
}

func (f *fakeStore) SaveClient(_ context.Context, id, agent string, hash []byte, operator bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clients[id] = storedClient{hash: hash, agent: agent, operator: operator}
	return nil
}

func (f *fakeStore) Client(_ context.Context, id string) (string, []byte, bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clients[id]
	return c.agent, c.hash, c.operator, ok, nil
}

func (f *fakeStore) AgentExists(_ context.Context, agent string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.existsErr != nil {
		return false, f.existsErr
	}
	for _, c := range f.clients {
		if c.agent == agent {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) SuspendAgent(_ context.Context, agent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.suspendErr != nil {
		return f.suspendErr
	}
	f.suspended[agent] = true
	return nil
}

func (f *fakeStore) ResumeAgent(_ context.Context, agent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resumeErr != nil {
		return f.resumeErr
	}
	delete(f.suspended, agent)
	return nil
}

func (f *fakeStore) AgentSuspended(_ context.Context, agent string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return false, f.readErr
	}
	return f.suspended[agent], nil
}
