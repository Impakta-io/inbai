// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

const mcpList = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":"inbox-a"}}}`

func TestSuspendedAgentIsRejectedOnRESTAndMCP(t *testing.T) {
	db := newCredentialStore()
	srv, mail := suspendServer(t, db)
	token := suspendTestAgentToken(t, srv.URL, "ada")

	unknown := operatorJSON(t, srv.URL, http.MethodPost, "/v1/agents/bob/suspend", "")
	if unknown.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown agent %d %s", unknown.StatusCode, readBody(t, unknown))
	}
	stopped := operatorJSON(t, srv.URL, http.MethodPost, "/v1/agents/ada/suspend", "")
	if stopped.StatusCode != http.StatusNoContent {
		t.Fatalf("suspend %d %s", stopped.StatusCode, readBody(t, stopped))
	}
	if res := agentGet(t, srv.URL, token, "/v1/inboxes/inbox-a/messages"); res.StatusCode != http.StatusUnauthorized || mail.listed {
		t.Fatalf("rest %d listed %v", res.StatusCode, mail.listed)
	}
	if res := agentJSON(t, srv.URL, token, http.MethodPost, "/mcp", mcpList); res.StatusCode != http.StatusUnauthorized || mail.listed {
		t.Fatalf("mcp %d listed %v", res.StatusCode, mail.listed)
	}
	if got := auditActions(t, srv.URL); got != "agent.create grant agent.suspend" {
		t.Fatalf("audit %q", got)
	}
}

func TestSuspendThatIsNotStoredFailsWithoutAudit(t *testing.T) {
	db := newCredentialStore()
	srv, _ := suspendServer(t, db)
	token := suspendTestAgentToken(t, srv.URL, "ada")

	db.setErrors(errors.New("connection refused"), nil)
	res := operatorJSON(t, srv.URL, http.MethodPost, "/v1/agents/ada/suspend", "")
	if res.StatusCode < 400 {
		t.Fatalf("suspend %d", res.StatusCode)
	}
	db.setErrors(nil, nil)
	if got := auditActions(t, srv.URL); strings.Contains(got, "agent.suspend") {
		t.Fatalf("audit %q", got)
	}
	if res := agentGet(t, srv.URL, token, "/v1/inboxes/inbox-a/messages"); res.StatusCode != http.StatusOK {
		t.Fatalf("agent %d", res.StatusCode)
	}
}

func TestTokenIsNotAcceptedWhenSuspensionCannotBeRead(t *testing.T) {
	db := newCredentialStore()
	srv, mail := suspendServer(t, db)
	token := suspendTestAgentToken(t, srv.URL, "ada")
	if res := operatorJSON(t, srv.URL, http.MethodPost, "/v1/agents/ada/suspend", ""); res.StatusCode != http.StatusNoContent {
		t.Fatalf("suspend %d", res.StatusCode)
	}

	db.setErrors(nil, errors.New("connection refused"))
	rest := agentGet(t, srv.URL, token, "/v1/inboxes/inbox-a/messages")
	if rest.StatusCode != http.StatusServiceUnavailable || mail.listed {
		t.Fatalf("rest %d listed %v %s", rest.StatusCode, mail.listed, readBody(t, rest))
	}
	if rest.Header.Get("WWW-Authenticate") != "" {
		t.Fatal("an unavailable store should not be an authentication challenge")
	}
	mcp := agentJSON(t, srv.URL, token, http.MethodPost, "/mcp", mcpList)
	if mcp.StatusCode != http.StatusServiceUnavailable || mail.listed {
		t.Fatalf("mcp %d listed %v %s", mcp.StatusCode, mail.listed, readBody(t, mcp))
	}
}

func suspendServer(t *testing.T, db auth.Store) (*httptest.Server, *fakeMail) {
	t.Helper()
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "hello"}}}
	dir := auth.NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	dir.SetDB(db)
	srv := httptest.NewServer((&Server{
		Service:   service.New(mail),
		Directory: dir,
		Token:     "operator-token",
		Issuer:    "http://127.0.0.1:8787",
	}).Handler())
	t.Cleanup(srv.Close)
	return srv, mail
}

func suspendTestAgentToken(t *testing.T, base, name string) string {
	t.Helper()
	create := operatorJSON(t, base, http.MethodPost, "/v1/agents", `{"name":"`+name+`"}`)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("agent %d %s", create.StatusCode, readBody(t, create))
	}
	var cred struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	mustJSON(t, create, &cred)
	grant := operatorJSON(t, base, http.MethodPost, "/v1/grants", `{"agent":"`+name+`","inbox":"inbox-a","permission":"read"}`)
	if grant.StatusCode != http.StatusCreated {
		t.Fatalf("grant %d %s", grant.StatusCode, readBody(t, grant))
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {cred.ID}, "client_secret": {cred.Secret}}
	res, err := http.Post(base+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	mustJSON(t, res, &tok)
	if tok.AccessToken == "" {
		t.Fatal("no access token")
	}
	return tok.AccessToken
}

func auditActions(t *testing.T, base string) string {
	t.Helper()
	var page struct {
		Events []struct {
			Action string `json:"action"`
		} `json:"events"`
	}
	mustJSON(t, agentGet(t, base, "operator-token", "/v1/audit"), &page)
	var actions []string
	for _, row := range page.Events {
		actions = append(actions, row.Action)
	}
	return strings.Join(actions, " ")
}

// credentialStore is an auth.Store in memory that fails on demand.
type credentialStore struct {
	mu         sync.Mutex
	clients    map[string]string
	hashes     map[string][]byte
	suspended  map[string]bool
	suspendErr error
	readErr    error
}

func newCredentialStore() *credentialStore {
	return &credentialStore{clients: map[string]string{}, hashes: map[string][]byte{}, suspended: map[string]bool{}}
}

func (c *credentialStore) setErrors(suspend, read error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.suspendErr, c.readErr = suspend, read
}

func (c *credentialStore) SaveClient(_ context.Context, id, agent string, hash []byte, _ bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clients[id], c.hashes[id] = agent, hash
	return nil
}

func (c *credentialStore) Client(_ context.Context, id string) (string, []byte, bool, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	agent, ok := c.clients[id]
	return agent, c.hashes[id], false, ok, nil
}

func (c *credentialStore) AgentExists(_ context.Context, agent string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, name := range c.clients {
		if name == agent {
			return true, nil
		}
	}
	return false, nil
}

func (c *credentialStore) SuspendAgent(_ context.Context, agent string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.suspendErr != nil {
		return c.suspendErr
	}
	c.suspended[agent] = true
	return nil
}

func (c *credentialStore) AgentSuspended(_ context.Context, agent string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return false, c.readErr
	}
	return c.suspended[agent], nil
}
