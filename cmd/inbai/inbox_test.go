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
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Errorf("authorization %q", r.Header.Get("Authorization"))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		paths = append(paths, r.Method+" "+r.URL.Path)
		defer func() { _ = r.Body.Close() }()
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/inboxes":
			if !strings.Contains(string(raw), `"localPart":"alice"`) {
				t.Errorf("create body %s", raw)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"alice-id","address":"alice@example.com"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages"):
			if !strings.Contains(string(raw), `"subject":"hello"`) {
				t.Errorf("send body %s", raw)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"status":"submitted"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/inboxes/alice-id/messages":
			_, _ = w.Write([]byte(`{"messages":[{"id":"m","subject":"hello","preview":"body"}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	path := writeOperatorConfig(t, srv.URL)
	env := func(key string) string {
		if key == "INBAI_OPERATOR_TOKEN" {
			return "operator-token"
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"inbox", "create", "--config", path, "Alice"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("create exit %d stderr %s", code, stderr.String())
	}
	if stdout.String() != "Created alice@example.com\n" {
		t.Fatalf("stdout %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"message", "send", "--config", path, "--from", "alice@example.com", "--to", "bob@example.com", "--subject", "hello", "--body", "body"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("send exit %d stderr %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"message", "list", "--config", path, "--json", "alice@example.com"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("list exit %d stderr %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"subject":"hello"`) {
		t.Fatalf("list stdout %s", stdout.String())
	}
	joined := strings.Join(paths, ",")
	for _, want := range []string{"POST /v1/inboxes", "POST /v1/inboxes/alice-id/messages", "GET /v1/inboxes/alice-id/messages"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("paths %s missing %s", joined, want)
		}
	}
}

func TestInboxGeneratePollsAJob(t *testing.T) {
	var jobs int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/inboxes/generate":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"jobId":"job-1","status":"running"}`))
		case "/v1/jobs/job-1":
			jobs++
			if jobs == 1 {
				_, _ = w.Write([]byte(`{"id":"job-1","status":"running","total":2,"created":1}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"job-1","status":"done","total":2,"created":2,"inboxes":[{"id":"id-0","address":"u00000@example.com"},{"id":"id-1","address":"u00001@example.com"}]}`))
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	path := writeOperatorConfig(t, srv.URL)
	var stdout, stderr bytes.Buffer
	code := run([]string{"inbox", "generate", "--config", path, "--count", "2"}, &stdout, &stderr, func(key string) string {
		if key == "INBAI_OPERATOR_TOKEN" {
			return "operator-token"
		}
		return ""
	})
	if code != 0 {
		t.Fatalf("exit %d stderr %s stdout %s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "created 1 of 2") || !strings.Contains(stdout.String(), "Created 2 addresses") {
		t.Fatalf("stdout %s", stdout.String())
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "u00000@example.com") {
		t.Fatalf("config %s", saved)
	}
}

func TestMessageCommandsTalkToTheAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/inboxes/alice-id/messages/m":
			_, _ = w.Write([]byte(`{"id":"m","subject":"hello","text":"body","attachments":[{"name":"note.txt","type":"text/plain","size":4}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reply"):
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"status":"submitted"}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw, err := json.Marshal(map[string]any{
		"apiUrl": srv.URL, "domain": "example.com",
		"inboxes": map[string]string{"alice@example.com": "alice-id"},
	})
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
	if !strings.Contains(stdout.String(), "hello") || !strings.Contains(stdout.String(), "attachment note.txt") {
		t.Fatalf("get stdout %s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"message", "reply", "--config", path, "--to", "bob@example.com", "--body", "ok", "alice@example.com", "m"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("reply %d %s", code, stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"message", "delete", "--config", path, "--json", "alice@example.com", "m"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("delete %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"deleted":"m"`) {
		t.Fatalf("delete stdout %s", stdout.String())
	}
}

func writeOperatorConfig(t *testing.T, apiURL string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw, err := json.Marshal(map[string]any{"apiUrl": apiURL, "domain": "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
