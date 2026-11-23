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

func TestInboxQuotaCommands(t *testing.T) {
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		if r.Method == http.MethodPut {
			_, _ = w.Write([]byte(`{"limitBytes":2048,"usedBytes":128}`))
			return
		}
		_, _ = w.Write([]byte(`{"limitBytes":104857600,"usedBytes":128}`))
	}))
	defer srv.Close()

	pathConfig := writeOperatorConfig(t, srv.URL)
	env := func(string) string { return "operator-token" }
	var stdout, stderr bytes.Buffer

	if code := run([]string{"inbox", "quota", "get", "--config", pathConfig, "inbox-a"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("get %d %s", code, stderr.String())
	}
	if method != http.MethodGet || path != "/v1/inboxes/inbox-a/quota" {
		t.Fatalf("get request %s %s", method, path)
	}
	if !strings.Contains(stdout.String(), "128 / 104857600 bytes") {
		t.Fatalf("get stdout %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"inbox", "quota", "set", "--config", pathConfig, "inbox-a", "2048"}, &stdout, &stderr, env); code != 0 {
		t.Fatalf("set %d %s", code, stderr.String())
	}
	if method != http.MethodPut || path != "/v1/inboxes/inbox-a/quota" || !strings.Contains(body, `"limitBytes":2048`) {
		t.Fatalf("set request %s %s %s", method, path, body)
	}
	if !strings.Contains(stdout.String(), "128 / 2048 bytes") {
		t.Fatalf("set stdout %s", stdout.String())
	}
}
