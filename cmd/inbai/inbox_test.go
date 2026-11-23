// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInboxCreateThenListAndSend(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := ioRead(r)
		if err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		if !strings.Contains(r.Header.Get("Authorization"), "Bearer key") {
			t.Errorf("authorization %q", r.Header.Get("Authorization"))
		}
		names := methodNames(t, body)
		calls = append(calls, names...)
		switch names[0] {
		case "x:Account/set":
			writeInit(w, names[0], "a", map[string]any{"created": map[string]any{"a": map[string]any{"id": "alice-id"}}})
		case "Identity/get":
			writeInit(w, names[0], "i", map[string]any{"list": []any{map[string]any{"id": "ident"}}})
		case "Mailbox/query":
			writeInit(w, names[0], "q", map[string]any{"ids": []string{"box"}})
		case "Email/set":
			writeMethods(w, []methodResult{
				{name: "Email/set", id: "e", payload: map[string]any{"created": map[string]any{"m": map[string]any{"id": "msg"}}}},
				{name: "EmailSubmission/set", id: "s", payload: map[string]any{"created": map[string]any{"s": map[string]any{"id": "sub"}}}},
			})
		case "Email/query":
			writeInit(w, names[0], "q", map[string]any{"ids": []string{"msg"}})
		case "Email/get":
			writeInit(w, names[0], "g", map[string]any{"list": []any{map[string]any{"id": "msg", "subject": "hello", "preview": "body"}}})
		default:
			t.Errorf("methods %v", names)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw, err := json.Marshal(map[string]any{
		"domain": "example.com", "domainId": "dom", "hostname": "mail.example.com",
		"stalwartUrl": srv.URL, "serviceAccountId": "svc", "operatorAccountId": "op",
		"inboxRoleId": "inbox-role", "serviceRoleId": "service-role", "apiKey": "key",
		"retentionDays": 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"inbox", "create", "--config", path, "Alice"}, &stdout, &stderr, nil); code != 0 {
		t.Fatalf("create exit %d stderr %s", code, stderr.String())
	}
	if stdout.String() != "Created alice@example.com\n" {
		t.Fatalf("stdout %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"message", "send", "--config", path, "--from", "alice@example.com", "--to", "bob@example.com", "--subject", "hello", "--body", "body"}, &stdout, &stderr, nil); code != 0 {
		t.Fatalf("send exit %d stderr %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"message", "list", "--config", path, "alice@example.com"}, &stdout, &stderr, nil); code != 0 {
		t.Fatalf("list exit %d stderr %s", code, stderr.String())
	}
	if stdout.String() != "hello\tbody\n" {
		t.Fatalf("list stdout %q", stdout.String())
	}
	joined := strings.Join(calls, ",")
	for _, want := range []string{"x:Account/set", "Identity/get", "Mailbox/query", "Email/set", "EmailSubmission/set", "Email/query", "Email/get"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("calls %s missing %s", joined, want)
		}
	}
}

type methodResult struct {
	name    string
	id      string
	payload map[string]any
}

func methodNames(t *testing.T, body []byte) []string {
	t.Helper()
	var req struct {
		MethodCalls [][]any `json:"methodCalls"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("request: %v", err)
	}
	names := make([]string, len(req.MethodCalls))
	for i, call := range req.MethodCalls {
		names[i], _ = call[0].(string)
	}
	return names
}

func writeMethods(w http.ResponseWriter, results []methodResult) {
	calls := make([]any, len(results))
	for i, result := range results {
		calls[i] = []any{result.name, result.payload, result.id}
	}
	raw, _ := json.Marshal(map[string]any{"methodResponses": calls})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func ioRead(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	return io.ReadAll(r.Body)
}
