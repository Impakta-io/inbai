// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

func TestAgentSendsOnlyAsTheInboxAndRepliesOnlyToTheSender(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "invoice", From: "Ada <ada@sender.test>"}}}
	dir := auth.NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	srv := httptest.NewServer((&Server{
		Service:   service.New(mail),
		Directory: dir,
		Token:     "operator-token",
		Issuer:    "http://127.0.0.1:8787",
		Domain:    "example.com",
	}).Handler())
	defer srv.Close()
	writer := grantedToken(t, srv.URL, "writer", "send")
	replier := grantedToken(t, srv.URL, "replier", "reply")

	forged := agentJSON(t, srv.URL, writer, http.MethodPost, "/v1/inboxes/inbox-a/messages", `{"from":"ceo@victim.test","to":"bob@example.com","subject":"hi"}`)
	if body := readBody(t, forged); forged.StatusCode != http.StatusForbidden || !strings.Contains(body, "not an address this inbox may send as") {
		t.Fatalf("forged from %d %s", forged.StatusCode, body)
	}
	operator := operatorJSON(t, srv.URL, http.MethodPost, "/v1/inboxes/inbox-a/messages", `{"from":"ceo@victim.test","to":"bob@example.com","subject":"hi"}`)
	if operator.StatusCode != http.StatusForbidden {
		t.Fatalf("operator forged from %d %s", operator.StatusCode, readBody(t, operator))
	}
	if mail.sent != 0 {
		t.Fatalf("a forged sender reached the backend %d times", mail.sent)
	}
	plain := agentJSON(t, srv.URL, writer, http.MethodPost, "/v1/inboxes/inbox-a/messages", `{"to":"bob@example.com","subject":"hi"}`)
	if plain.StatusCode != http.StatusAccepted || mail.last.From != "inbox-a@example.com" {
		t.Fatalf("send without from %d %s sent as %q", plain.StatusCode, readBody(t, plain), mail.last.From)
	}
	mcp := agentJSON(t, srv.URL, writer, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"send_message","arguments":{"inbox":"inbox-a","to":"bob@example.com","subject":"hi"}}}`)
	if body := readBody(t, mcp); mcp.StatusCode != http.StatusOK || strings.Contains(body, `"isError":true`) || mail.last.From != "inbox-a@example.com" {
		t.Fatalf("mcp send without from %d %s", mcp.StatusCode, body)
	}

	sent := mail.sent
	elsewhere := agentJSON(t, srv.URL, replier, http.MethodPost, "/v1/inboxes/inbox-a/messages/m/reply", `{"to":"eve@elsewhere.test","text":"the subject"}`)
	if body := readBody(t, elsewhere); elsewhere.StatusCode != http.StatusForbidden || !strings.Contains(body, "needs send") {
		t.Fatalf("reply elsewhere %d %s", elsewhere.StatusCode, body)
	}
	if mail.sent != sent {
		t.Fatal("a refused reply reached the backend")
	}
	back := agentJSON(t, srv.URL, replier, http.MethodPost, "/v1/inboxes/inbox-a/messages/m/reply", `{"text":"thanks"}`)
	if back.StatusCode != http.StatusAccepted || mail.last.To != "ada@sender.test" || mail.last.Subject != "Re: invoice" {
		t.Fatalf("reply to sender %d %s last %+v", back.StatusCode, readBody(t, back), mail.last)
	}

	mail.messages[0].From = ""
	nowhere := agentJSON(t, srv.URL, replier, http.MethodPost, "/v1/inboxes/inbox-a/messages/m/reply", `{"text":"thanks"}`)
	if body := readBody(t, nowhere); nowhere.StatusCode != http.StatusBadRequest || !strings.Contains(body, "no From or Reply-To") {
		t.Fatalf("reply without a sender %d %s", nowhere.StatusCode, body)
	}
}

// grantedToken creates an agent, grants it perm on inbox-a, and returns its access token.
func grantedToken(t *testing.T, base, name, perm string) string {
	t.Helper()
	token := agentToken(t, base, name)
	grant := operatorJSON(t, base, http.MethodPost, "/v1/grants", `{"agent":"`+name+`","inbox":"inbox-a","permission":"`+perm+`"}`)
	if grant.StatusCode != http.StatusCreated {
		t.Fatalf("grant %d %s", grant.StatusCode, readBody(t, grant))
	}
	return token
}
