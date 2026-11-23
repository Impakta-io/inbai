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

func TestAuditCommandPrintsTheLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audit" || r.URL.Query().Get("after") != "4" {
			t.Errorf("path %s query %s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"events":[{"id":5,"actor":"operator","action":"grant","target":"ada inbox-a read"}]}`))
	}))
	defer srv.Close()
	path := writeOperatorConfig(t, srv.URL)
	var stdout, stderr bytes.Buffer
	code := run([]string{"audit", "--config", path, "--after", "4"}, &stdout, &stderr, func(string) string { return "operator-token" })
	if code != 0 {
		t.Fatalf("audit %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "5\tgrant\toperator\tada inbox-a read") {
		t.Fatalf("stdout %s", stdout.String())
	}
}
