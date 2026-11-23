// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLogUsesTheCallerID(t *testing.T) {
	var logs bytes.Buffer
	srv := httptest.NewServer((&Server{Log: &logs}).Handler())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Request-Id", "trace-1")
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Request-Id") != "trace-1" {
		t.Fatalf("status %d id %q", resp.StatusCode, resp.Header.Get("X-Request-Id"))
	}
	var line struct {
		RequestID string `json:"requestId"`
		Method    string `json:"method"`
		Path      string `json:"path"`
		Status    int    `json:"status"`
	}
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("log %s", logs.String())
	}
	if line.RequestID != "trace-1" || line.Method != http.MethodGet || line.Path != "/healthz" || line.Status != http.StatusOK {
		t.Fatalf("line %+v", line)
	}
	if strings.Contains(logs.String(), "secret-token") || strings.Contains(logs.String(), "Authorization") {
		t.Fatalf("log included a credential: %s", logs.String())
	}
}

func TestRequestLogReplacesAnUnsafeID(t *testing.T) {
	var logs bytes.Buffer
	req := httptest.NewRequest(http.MethodGet, "/healthz?token=secret", nil)
	req.Header["X-Request-Id"] = []string{"bad\nid"}
	rec := httptest.NewRecorder()
	(&Server{Log: &logs}).Handler().ServeHTTP(rec, req)
	id := rec.Header().Get("X-Request-Id")
	if id == "" || strings.Contains(id, "\n") || id == "bad\nid" {
		t.Fatalf("id %q", id)
	}
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "token=") {
		t.Fatalf("log included the query: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"`+id+`"`) {
		t.Fatalf("log %s", logs.String())
	}
}

func TestRequestLogRecordsARejectedCall(t *testing.T) {
	var logs bytes.Buffer
	srv := httptest.NewServer((&Server{Log: &logs, Token: "operator"}).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/inboxes")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var line struct {
		Path   string `json:"path"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &line); err != nil {
		t.Fatalf("log %s", logs.String())
	}
	if line.Path != "/v1/inboxes" || line.Status != http.StatusUnauthorized {
		t.Fatalf("line %+v", line)
	}
}
