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
	"path/filepath"
	"strings"
	"testing"
)

func attachmentServer(size int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := bytes.Repeat([]byte("x"), 1<<20)
		for sent := 0; sent < size; sent += len(chunk) {
			_, _ = w.Write(chunk[:min(len(chunk), size-sent)])
		}
	}))
}

func TestDownloadReturnsTwentyMiB(t *testing.T) {
	srv := attachmentServer(20 << 20)
	defer srv.Close()
	got, err := apiClient{base: srv.URL, token: "t"}.download(context.Background(), "a", "m", "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20<<20 {
		t.Fatalf("got %d bytes", len(got))
	}
}

func TestAttachmentOverTheLimitWritesNoFile(t *testing.T) {
	srv := attachmentServer(maxResponseBytes + 1)
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	raw, err := json.Marshal(map[string]any{
		"apiUrl": srv.URL, "domain": "example.com",
		"inboxes": map[string]string{"alice@example.com": "alice-id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.bin")
	var stdout, stderr bytes.Buffer
	code := run([]string{"message", "attachment", "--config", cfg, "--output", out, "alice@example.com", "m", "b"}, &stdout, &stderr, func(string) string { return "t" })
	if code == 0 || !strings.Contains(stderr.String(), "larger than") {
		t.Fatalf("code %d stderr %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "Wrote") {
		t.Fatalf("stdout %s", stdout.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("file exists: %v", err)
	}
}
