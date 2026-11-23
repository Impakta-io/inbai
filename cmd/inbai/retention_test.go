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

func TestRetentionCommandSetsThePeriod(t *testing.T) {
	var ran bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		switch r.Method + " " + r.URL.Path {
		case http.MethodPut + " /v1/retention":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"days":14,"inbox":"","override":true}`))
		case http.MethodPost + " /v1/retention/run":
			ran = true
			_, _ = w.Write([]byte(`{"deleted":2}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	path := writeOperatorConfig(t, srv.URL)
	env := func(string) string { return "operator-token" }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"retention", "--config", path, "--days", "14"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("set %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Server retention is 14 days.") {
		t.Fatalf("stdout %s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"retention", "run", "--config", path, "--json"}, &stdout, &stderr, env); code != 0 || !ran {
		t.Fatalf("run %d ran %v %s", code, ran, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"deleted":2`) {
		t.Fatalf("run stdout %s", stdout.String())
	}
}
