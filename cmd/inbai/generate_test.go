// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func runGenerate(t *testing.T, handler http.HandlerFunc, args ...string) (int, string, string, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	path := writeOperatorConfig(t, srv.URL)
	var stdout, stderr bytes.Buffer
	full := append([]string{"inbox", "generate", "--config", path}, args...)
	code := run(full, &stdout, &stderr, func(key string) string {
		if key == "INBAI_OPERATOR_TOKEN" {
			return "operator-token"
		}
		return ""
	})
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return code, stdout.String(), stderr.String(), string(saved)
}

func TestInboxGenerateReportsExistingOnDirectCreate(t *testing.T) {
	code, out, errOut, cfg := runGenerate(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"inboxes":[{"id":"id-2","address":"u00002@example.com"}],"existing":["u00000@example.com","u00001@example.com"]}`))
	}, "--count", "3")
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, errOut)
	}
	for _, want := range []string{"Created 1 addresses", "Skipped 2 that already exist: u00000@example.com, u00001@example.com"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout %q missing %q", out, want)
		}
	}
	if !strings.Contains(cfg, "u00002@example.com") || strings.Contains(cfg, "u00000@example.com") {
		t.Fatalf("config records skipped addresses: %s", cfg)
	}
}

func TestInboxGenerateReportsExistingFromJob(t *testing.T) {
	code, out, errOut, cfg := runGenerate(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/inboxes/generate" {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"jobId":"job-1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"done","total":2,"created":1,"inboxes":[{"id":"id-1","address":"u00001@example.com"}],"existing":["u00000@example.com"]}`))
	}, "--count", "2", "--json")
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, errOut)
	}
	var got struct {
		Inboxes  []map[string]any `json:"inboxes"`
		Existing []string         `json:"existing"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout %q: %v", out, err)
	}
	if len(got.Inboxes) != 1 || len(got.Existing) != 1 || got.Existing[0] != "u00000@example.com" {
		t.Fatalf("got %+v", got)
	}
	if strings.Contains(cfg, "u00000@example.com") {
		t.Fatalf("config records skipped address: %s", cfg)
	}
}

func TestInboxGenerateOldServerHasNoExisting(t *testing.T) {
	old := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"inboxes":[{"id":"id-0","address":"u00000@example.com"}]}`))
	}
	code, out, errOut, _ := runGenerate(t, old, "--count", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, errOut)
	}
	if !strings.Contains(out, `"existing":[]`) {
		t.Fatalf("stdout %q", out)
	}
	code, out, _, _ = runGenerate(t, old, "--count", "1")
	if code != 0 || strings.Contains(out, "Skipped") {
		t.Fatalf("exit %d stdout %q", code, out)
	}
}

func TestInboxGenerateAllExistFails(t *testing.T) {
	code, out, errOut, _ := runGenerate(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"title":"Conflict","status":409,"detail":"all 3 addresses already exist"}`))
	}, "--count", "3")
	if code != 1 || !strings.Contains(errOut, "all 3 addresses already exist") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}
