// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
)

// internalFailure is the kind of text a backend or database error carries.
const internalFailure = `dial tcp stalwart.internal.example (10.20.30.40:8080): pq: password authentication failed for user "inbai_admin"`

var internalFragments = []string{"stalwart.internal.example", "10.20.30.40", "password authentication", "inbai_admin"}

// failingMail is a mail backend whose sends and lists fail with err.
type failingMail struct {
	*fakeMail
	err error
}

func (f *failingMail) Deliver(context.Context, mailbackend.Outbound) error { return f.err }

func (f *failingMail) List(context.Context, string, mailbackend.Page) ([]mailbackend.Message, string, error) {
	return nil, "", f.err
}

const sendJSON = `{"from":"alice@example.com","to":"bob@example.com","subject":"hi","text":"x"}`

func serveOnce(t *testing.T, handler http.Handler, method, path, body, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer operator-token")
	req.Header.Set("X-Request-Id", id)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

type logLine struct {
	RequestID string `json:"requestId"`
	Status    int    `json:"status"`
	Error     string `json:"error"`
}

func logFor(t *testing.T, logs *bytes.Buffer, id string) logLine {
	t.Helper()
	for _, raw := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var line logLine
		if err := json.Unmarshal(raw, &line); err != nil {
			t.Fatalf("log line %s", raw)
		}
		if line.RequestID == id {
			return line
		}
	}
	t.Fatalf("no log line for %s in %s", id, logs.String())
	return logLine{}
}

func assertHidden(t *testing.T, body string) {
	t.Helper()
	for _, fragment := range internalFragments {
		if strings.Contains(body, fragment) {
			t.Fatalf("response shows %q: %s", fragment, body)
		}
	}
}

func assertLogged(t *testing.T, line logLine) {
	t.Helper()
	for _, fragment := range internalFragments {
		if !strings.Contains(line.Error, fragment) {
			t.Fatalf("log line %+v lacks %q", line, fragment)
		}
	}
}

func TestUnexpectedErrorIsLoggedAndNotShown(t *testing.T) {
	var logs bytes.Buffer
	mail := &failingMail{fakeMail: &fakeMail{id: "alice-id"}, err: errors.New(internalFailure)}
	handler := (&Server{Service: service.New(mail), Token: "operator-token", Domain: "example.com", Log: &logs}).Handler()

	rec := serveOnce(t, handler, http.MethodPost, "/v1/inboxes/alice-id/messages", sendJSON, "rest-1")
	var problem struct {
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusBadGateway || problem.Status != http.StatusBadGateway {
		t.Fatalf("send %d %s", rec.Code, rec.Body.String())
	}
	assertHidden(t, rec.Body.String())
	if problem.Detail != "internal error, see the server log for request id rest-1" {
		t.Fatalf("detail %q", problem.Detail)
	}
	assertLogged(t, logFor(t, &logs, "rest-1"))

	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":"alice-id"}}}`
	rec = serveOnce(t, handler, http.MethodPost, "/mcp", call, "mcp-1")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"isError":true`) || !strings.Contains(rec.Body.String(), "request id mcp-1") {
		t.Fatalf("mcp %d %s", rec.Code, rec.Body.String())
	}
	assertHidden(t, rec.Body.String())
	assertLogged(t, logFor(t, &logs, "mcp-1"))
}

func TestRefusedSendShowsTheReason(t *testing.T) {
	var logs bytes.Buffer
	refused := &mailbackend.RefusedError{Type: "forbiddenFrom", Description: "Sender not allowed"}
	mail := &failingMail{fakeMail: &fakeMail{id: "alice-id"}, err: fmt.Errorf("stalwart s: %w (%s)", refused, internalFailure)}
	handler := (&Server{Service: service.New(mail), Token: "operator-token", Domain: "example.com", Log: &logs}).Handler()

	rec := serveOnce(t, handler, http.MethodPost, "/v1/inboxes/alice-id/messages", sendJSON, "rest-2")
	want := "mail server refused the request: forbiddenFrom: Sender not allowed"
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), `"detail":"`+want+`"`) {
		t.Fatalf("send %d %s", rec.Code, rec.Body.String())
	}
	assertHidden(t, rec.Body.String())
	assertLogged(t, logFor(t, &logs, "rest-2"))

	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"send_message","arguments":{"inbox":"alice-id","from":"alice@example.com","to":"bob@example.com","subject":"hi"}}}`
	rec = serveOnce(t, handler, http.MethodPost, "/mcp", call, "mcp-2")
	if !strings.Contains(rec.Body.String(), `"isError":true`) || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("mcp %d %s", rec.Code, rec.Body.String())
	}
	assertHidden(t, rec.Body.String())
}

func TestServiceErrorsKeepTheirText(t *testing.T) {
	mail := &fakeMail{id: "alice-id"}
	handler := (&Server{Service: service.New(mail), Token: "operator-token", Domain: "example.com"}).Handler()
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait_for_message","arguments":{"inbox":"alice-id","timeoutSeconds":0.01}}}`
	rec := serveOnce(t, handler, http.MethodPost, "/mcp", call, "mcp-3")
	if !strings.Contains(rec.Body.String(), `"isError":true`) || !strings.Contains(rec.Body.String(), service.ErrUnavailable.Error()) {
		t.Fatalf("mcp %d %s", rec.Code, rec.Body.String())
	}
}

// downLog is an event log whose database does not answer.
type downLog struct {
	*store.Memory
}

func (downLog) Ping(context.Context) error { return errors.New(internalFailure) }

func TestReadyNamesTheDependencyNotTheError(t *testing.T) {
	var logs bytes.Buffer
	svc := service.New(&fakeMail{})
	svc.SetFeed(events.New(downLog{Memory: &store.Memory{}}))
	handler := (&Server{Service: svc, Log: &logs}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	req.Header.Set("X-Request-Id", "ready-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"detail":"event database is not reachable"`) {
		t.Fatalf("ready %d %s", rec.Code, rec.Body.String())
	}
	assertHidden(t, rec.Body.String())
	assertLogged(t, logFor(t, &logs, "ready-1"))
}

func TestTokenRejectsAnOversizedBody(t *testing.T) {
	handler := (&Server{Directory: auth.NewDirectory(bytes.Repeat([]byte("k"), 32), "http://127.0.0.1:8787")}).Handler()
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {"nobody"}, "client_secret": {"wrong"}}
	if rec := post(form.Encode()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("small form %d %s", rec.Code, rec.Body.String())
	}
	form.Set("client_secret", strings.Repeat("x", tokenBodyLimit))
	if rec := post(form.Encode()); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Fatalf("oversized form %d %s", rec.Code, rec.Body.String())
	}
}
