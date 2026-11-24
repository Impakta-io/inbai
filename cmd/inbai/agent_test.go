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

func TestAgentCommandsTalkToTheAPI(t *testing.T) {
	var revoked, resumed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agents":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"client","secret":"shown-once","agent":"ada"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/grants":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"agent":"ada","inbox":"inbox-a","permission":"read"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agents/ada/suspend":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agents/ada/resume":
			resumed = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agents/bob/resume":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"title":"Not found","status":404,"detail":"not found: agent bob has no credential"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/grants/ada/inbox-a/read":
			revoked = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	path := writeOperatorConfig(t, srv.URL)
	env := func(string) string { return "operator-token" }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"agent", "create", "--config", path, "--json", "ada"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("create %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "shown-once") {
		t.Fatalf("create stdout %s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"agent", "grant", "--config", path, "ada", "inbox-a", "read"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("grant %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Granted read on inbox-a to ada") {
		t.Fatalf("grant stdout %s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"agent", "suspend", "--config", path, "--json", "ada"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("suspend %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"suspended":"ada"`) {
		t.Fatalf("suspend stdout %s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"agent", "resume", "--config", path, "--json", "ada"}, &stdout, &stderr, env); code != 0 || !resumed {
		t.Fatalf("resume %d resumed %v %s", code, resumed, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"resumed":"ada"`) {
		t.Fatalf("resume stdout %s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"agent", "resume", "--config", path, "ada"}, &stdout, &stderr, env); code != 0 || !strings.Contains(stdout.String(), "Resumed ada") {
		t.Fatalf("resume text %d %s %s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"agent", "resume", "--config", path, "bob"}, &stdout, &stderr, env); code != 1 || !strings.Contains(stderr.String(), "agent resume:") {
		t.Fatalf("resume unknown %d %s", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"agent", "resume", "--config", path}, &stdout, &stderr, env); code != 2 || !strings.Contains(stderr.String(), "usage: inbai agent resume <name>") {
		t.Fatalf("resume usage %d %s", code, stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"agent", "revoke", "--config", path, "ada", "inbox-a", "read"}, &stdout, &stderr, env); code != 0 || !revoked {
		t.Fatalf("revoke %d revoked %v %s", code, revoked, stderr.String())
	}
}
