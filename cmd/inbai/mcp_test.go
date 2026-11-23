// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPServeExchangesAClientCredential(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"agent-token","expires_in":600,"token_type":"Bearer"}`))
		case "/v1/inboxes/inbox-a/messages":
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"messages":[{"id":"m","subject":"secret","preview":"body"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	path := writeOperatorConfig(t, srv.URL)
	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":"inbox-a"}}}`,
	}, "\n"))
	var stdout, stderr bytes.Buffer
	code := mcpCmd([]string{"serve", "--config", path}, stdin, &stdout, &stderr, func(key string) string {
		switch key {
		case "INBAI_CLIENT_ID":
			return "id"
		case "INBAI_CLIENT_SECRET":
			return "secret"
		default:
			return ""
		}
	})
	if code != 0 {
		t.Fatalf("code %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "2026-07-28") || !strings.Contains(stdout.String(), "secret") || !strings.Contains(stdout.String(), "externalContent") {
		t.Fatalf("stdout %s", stdout.String())
	}
	if gotAuth != "Bearer agent-token" {
		t.Fatalf("auth %q", gotAuth)
	}
}

func TestMCPServeRequiresACredential(t *testing.T) {
	path := writeOperatorConfig(t, "http://127.0.0.1:8787")
	var stdout, stderr bytes.Buffer
	code := mcpCmd([]string{"serve", "--config", path}, strings.NewReader(""), &stdout, &stderr, func(string) string { return "" })
	if code != 2 || !strings.Contains(stderr.String(), "missing credential") {
		t.Fatalf("code %d %s", code, stderr.String())
	}
}
