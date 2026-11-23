// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAllowlistCommandReplacesTheList(t *testing.T) {
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		method = r.Method
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"senders":["ada@example.com","example.com"],"set":true}`))
	}))
	defer srv.Close()
	pathConfig := writeOperatorConfig(t, srv.URL)
	env := func(string) string { return "operator-token" }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"inbox", "allowlist", "set", "--config", pathConfig, "inbox-a", "ada@example.com", "example.com"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("set %d %s", code, stderr.String())
	}
	if method != http.MethodPut || path != "/v1/inboxes/inbox-a/allowlist" || !strings.Contains(body, "ada@example.com") {
		t.Fatalf("request %s %s %s", method, path, body)
	}
	if !strings.Contains(stdout.String(), "Sender allowlist has 2 entries.") {
		t.Fatalf("stdout %s", stdout.String())
	}
}
