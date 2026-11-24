// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/mcp"
)

func TestMCPStdioGetMessageCarriesTextTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"m","subject":"big","text":"abc...","textTruncated":true}`))
	}))
	defer srv.Close()
	box := apiBox{client: apiClient{base: srv.URL, token: "t"}}
	got, _ := mcp.Dispatch(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_message","arguments":{"inbox":"inbox-a","messageId":"m"}}}`), box)
	if !strings.Contains(string(got), `textTruncated\":true`) {
		t.Fatalf("get %s", got)
	}
}

func TestMessageGetNotesATruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","subject":"big","text":"abc...","textTruncated":true}`))
	}))
	defer srv.Close()
	path := writeOperatorConfig(t, srv.URL)
	raw, err := json.Marshal(map[string]any{"apiUrl": srv.URL, "domain": "example.com", "inboxes": map[string]string{"alice@example.com": "alice-id"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(string) string { return "operator-token" }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"message", "get", "--config", path, "alice@example.com", "m"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("get %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cut at the 1 MiB limit") {
		t.Fatalf("stdout %s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"message", "get", "--json", "--config", path, "alice@example.com", "m"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("get json %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"textTruncated":true`) || strings.Contains(stdout.String(), "cut at") {
		t.Fatalf("json stdout %s", stdout.String())
	}
}
