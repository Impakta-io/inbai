// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

func TestAgentTokenCannotReadAnotherInbox(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "secret", Preview: "body"}}}
	dir := auth.NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	handler := (&Server{
		Service:   service.New(mail),
		Directory: dir,
		Token:     "operator-token",
		Issuer:    "http://127.0.0.1:8787",
		Domain:    "example.com",
	}).Handler()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	meta, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, meta)
	if meta.StatusCode != http.StatusOK || !strings.Contains(body, "/oauth/token") {
		t.Fatalf("metadata %d %s", meta.StatusCode, body)
	}

	create := operatorJSON(t, srv.URL, http.MethodPost, "/v1/agents", `{"name":"ada"}`)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("agent %d %s", create.StatusCode, readBody(t, create))
	}
	var cred struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	mustJSON(t, create, &cred)
	grant := operatorJSON(t, srv.URL, http.MethodPost, "/v1/grants", `{"agent":"ada","inbox":"inbox-a","permission":"read"}`)
	if grant.StatusCode != http.StatusCreated {
		t.Fatalf("grant %d %s", grant.StatusCode, readBody(t, grant))
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", cred.ID)
	form.Set("client_secret", cred.Secret)
	res, err := http.Post(srv.URL+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	mustJSON(t, res, &tok)
	if tok.AccessToken == "" || tok.ExpiresIn != 600 {
		t.Fatalf("token %+v", tok)
	}

	allowed := agentGet(t, srv.URL, tok.AccessToken, "/v1/inboxes/inbox-a/messages")
	if allowed.StatusCode != http.StatusOK || !strings.Contains(readBody(t, allowed), "secret") {
		t.Fatalf("allowed %d", allowed.StatusCode)
	}
	mail.listed = false
	denied := agentGet(t, srv.URL, tok.AccessToken, "/v1/inboxes/inbox-b/messages")
	if denied.StatusCode != http.StatusForbidden || mail.listed {
		t.Fatalf("denied %d listed %v body %s", denied.StatusCode, mail.listed, readBody(t, denied))
	}
	if denied.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("forbidden should not be an authentication challenge")
	}

	self := agentJSON(t, srv.URL, tok.AccessToken, http.MethodPost, "/v1/grants", `{"agent":"ada","inbox":"inbox-b","permission":"read"}`)
	if self.StatusCode != http.StatusForbidden {
		t.Fatalf("self grant %d %s", self.StatusCode, readBody(t, self))
	}

	allowedMCP := agentJSON(t, srv.URL, tok.AccessToken, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":"inbox-a"}}}`)
	if allowedMCP.StatusCode != http.StatusOK || !strings.Contains(readBody(t, allowedMCP), "secret") {
		t.Fatalf("mcp allowed %d", allowedMCP.StatusCode)
	}
	mail.listed = false
	deniedMCP := agentJSON(t, srv.URL, tok.AccessToken, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":"inbox-b"}}}`)
	deniedBody := readBody(t, deniedMCP)
	if deniedMCP.StatusCode != http.StatusOK || !strings.Contains(deniedBody, `"isError":true`) || mail.listed {
		t.Fatalf("mcp denied %d listed %v body %s", deniedMCP.StatusCode, mail.listed, deniedBody)
	}
}

func TestAgentTokenCannotReadAnotherCallersJob(t *testing.T) {
	svc := service.New(&fakeMail{})
	svc.SetSyncLimit(1)
	dir := auth.NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	srv := httptest.NewServer((&Server{
		Service:   svc,
		Directory: dir,
		Token:     "operator-token",
		Issuer:    "http://127.0.0.1:8787",
		Domain:    "example.com",
	}).Handler())
	defer srv.Close()

	ada := agentToken(t, srv.URL, "ada")
	bob := agentToken(t, srv.URL, "bob")
	for _, agent := range []string{"ada", "bob"} {
		grant := operatorJSON(t, srv.URL, http.MethodPost, "/v1/grants", `{"agent":"`+agent+`","inbox":"*","permission":"inbox:provision"}`)
		if grant.StatusCode != http.StatusCreated {
			t.Fatalf("grant %s %d %s", agent, grant.StatusCode, readBody(t, grant))
		}
	}
	eve := agentToken(t, srv.URL, "eve")

	started := agentJSON(t, srv.URL, ada, http.MethodPost, "/v1/inboxes/generate", `{"prefix":"u","count":2}`)
	if started.StatusCode != http.StatusAccepted {
		t.Fatalf("generate %d %s", started.StatusCode, readBody(t, started))
	}
	var accepted struct {
		JobID string `json:"jobId"`
	}
	mustJSON(t, started, &accepted)
	path := "/v1/jobs/" + accepted.JobID
	deadline := time.Now().Add(time.Second)
	for {
		res := agentGet(t, srv.URL, "operator-token", path)
		body := readBody(t, res)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("operator read %d %s", res.StatusCode, body)
		}
		if strings.Contains(body, `"done"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish: %s", body)
		}
		time.Sleep(5 * time.Millisecond)
	}

	own := agentGet(t, srv.URL, ada, path)
	if body := readBody(t, own); own.StatusCode != http.StatusOK || !strings.Contains(body, "u00001@example.com") {
		t.Fatalf("starting agent %d %s", own.StatusCode, body)
	}
	unknown := agentGet(t, srv.URL, bob, "/v1/jobs/0000000000000000")
	unknownBody := readBody(t, unknown)
	if unknown.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id %d %s", unknown.StatusCode, unknownBody)
	}
	for name, token := range map[string]string{"provisioning agent": bob, "agent with no grants": eve} {
		res := agentGet(t, srv.URL, token, path)
		body := readBody(t, res)
		if res.StatusCode != http.StatusNotFound || strings.Contains(body, "example.com") || body != unknownBody {
			t.Fatalf("%s read another agent's job: %d %s", name, res.StatusCode, body)
		}
	}
}

// agentToken creates an agent credential and exchanges it for an access token.
func agentToken(t *testing.T, base, name string) string {
	t.Helper()
	create := operatorJSON(t, base, http.MethodPost, "/v1/agents", `{"name":"`+name+`"}`)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("agent %s %d %s", name, create.StatusCode, readBody(t, create))
	}
	var cred struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	mustJSON(t, create, &cred)
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", cred.ID)
	form.Set("client_secret", cred.Secret)
	res, err := http.Post(base+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	mustJSON(t, res, &tok)
	if tok.AccessToken == "" {
		t.Fatalf("no token for %s", name)
	}
	return tok.AccessToken
}

func operatorJSON(t *testing.T, base, method, path, body string) *http.Response {
	t.Helper()
	return agentJSON(t, base, "operator-token", method, path, body)
}

func agentJSON(t *testing.T, base, token, method, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func agentGet(t *testing.T, base, token, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func mustJSON(t *testing.T, res *http.Response, dest any) {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		t.Fatalf("json %s: %v", raw, err)
	}
}
