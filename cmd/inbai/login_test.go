// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoginWithOAuthAndSubsequentCommand(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			if r.PostForm.Get("client_id") != "cli-1" || r.PostForm.Get("client_secret") != "top-secret" {
				http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"short-jwt","token_type":"bearer","expires_in":600}`))
		case "/v1/inboxes":
			seenAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"inboxes":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	var stdout, stderr bytes.Buffer
	env := func(key string) string {
		if key == "INBAI_CLIENT_SECRET" {
			return "top-secret"
		}
		return ""
	}
	code := run([]string{"login", "--url", srv.URL, "--client-id", "cli-1", "--config", cfgPath, "--json"}, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("login %d: %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "top-secret") {
		t.Fatalf("login output leaked secret: %s", stdout.String())
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || out["auth"] != "client_credentials" || out["clientId"] != "cli-1" {
		t.Fatalf("login json %s err %v", stdout.String(), err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("config mode %o, want 600", info.Mode().Perm())
		}
	}

	// Subsequent command works with empty env by exchanging the stored OAuth credential.
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"inbox", "list", "--config", cfgPath}, &stdout, &stderr, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("inbox list %d: %s", code, stderr.String())
	}
	if seenAuth != "Bearer short-jwt" {
		t.Fatalf("auth = %q, want Bearer short-jwt", seenAuth)
	}

	// Clearing removes stored credentials so a subsequent command asks for login again.
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"login", "--clear", "--config", cfgPath}, &stdout, &stderr, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("login clear %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"inbox", "list", "--config", cfgPath}, &stdout, &stderr, func(string) string { return "" })
	if code != 2 {
		t.Fatalf("after clear expected exit 2, got %d", code)
	}
}

func TestLoginWithOperatorToken(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer op-tok" {
			http.Error(w, `{"title":"Unauthorized","status":401}`, http.StatusUnauthorized)
			return
		}
		seenAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/v1/retention":
			_, _ = w.Write([]byte(`{"days":30,"inbox":"","override":false}`))
		case "/v1/inboxes":
			_, _ = w.Write([]byte(`{"inboxes":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	var stdout, stderr bytes.Buffer
	env := func(key string) string {
		if key == "INBAI_OPERATOR_TOKEN" {
			return "op-tok"
		}
		return ""
	}
	code := run([]string{"login", "--url", srv.URL, "--config", cfgPath, "--json"}, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("login %d: %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "op-tok") {
		t.Fatalf("login output leaked token: %s", stdout.String())
	}

	// Works without INBAI_OPERATOR_TOKEN in env now.
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"inbox", "list", "--config", cfgPath}, &stdout, &stderr, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("inbox list %d: %s", code, stderr.String())
	}
	if seenAuth != "Bearer op-tok" {
		t.Fatalf("auth = %q, want Bearer op-tok", seenAuth)
	}

	// Invalid token is rejected and not saved.
	badCfg := filepath.Join(t.TempDir(), "bad.json")
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"login", "--url", srv.URL, "--config", badCfg}, &stdout, &stderr, func(key string) string {
		if key == "INBAI_OPERATOR_TOKEN" {
			return "wrong"
		}
		return ""
	})
	if code != 1 {
		t.Fatalf("bad token exit %d, want 1", code)
	}
	if _, err := os.Stat(badCfg); !os.IsNotExist(err) {
		t.Fatalf("bad login should not write config file")
	}
}

func TestLoginKeepsOperatorToken(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_, _ = w.Write([]byte(`{"access_token":"agent-jwt","token_type":"bearer","expires_in":600}`))
		case "/v1/inboxes":
			seenAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"inboxes":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"apiUrl":"`+srv.URL+`","operatorToken":"server-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	noEnv := func(string) string { return "" }
	secretEnv := func(key string) string {
		if key == "INBAI_CLIENT_SECRET" {
			return "s3cret"
		}
		return ""
	}
	if code := run([]string{"login", "--client-id", "cli-1", "--config", cfgPath}, &stdout, &stderr, secretEnv); code != 0 {
		t.Fatalf("login %d: %s", code, stderr.String())
	}
	cfg, err := readConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OperatorToken != "server-token" || cfg.ClientID != "cli-1" {
		t.Fatalf("after login: operatorToken %q clientId %q", cfg.OperatorToken, cfg.ClientID)
	}

	// The CLI acts as the client, not as the stored operator token.
	if code := run([]string{"inbox", "list", "--config", cfgPath}, &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("inbox list %d: %s", code, stderr.String())
	}
	if seenAuth != "Bearer agent-jwt" {
		t.Fatalf("auth = %q, want Bearer agent-jwt", seenAuth)
	}

	// Environment variables still win over anything stored.
	envOp := func(key string) string {
		if key == "INBAI_OPERATOR_TOKEN" {
			return "env-token"
		}
		return ""
	}
	if code := run([]string{"inbox", "list", "--config", cfgPath}, &stdout, &stderr, envOp); code != 0 {
		t.Fatalf("inbox list %d: %s", code, stderr.String())
	}
	if seenAuth != "Bearer env-token" {
		t.Fatalf("auth = %q, want Bearer env-token", seenAuth)
	}

	// Clear drops the client credentials and keeps the operator token.
	stdout.Reset()
	if code := run([]string{"login", "--clear", "--config", cfgPath}, &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("clear %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "client credentials") {
		t.Fatalf("clear output %q does not say what was cleared", stdout.String())
	}
	cfg, err = readConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientID != "" || cfg.ClientSecret != "" || cfg.OperatorToken != "server-token" {
		t.Fatalf("after clear: %+v", cfg)
	}
	if code := run([]string{"inbox", "list", "--config", cfgPath}, &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("inbox list %d: %s", code, stderr.String())
	}
	if seenAuth != "Bearer server-token" {
		t.Fatalf("auth after clear = %q, want Bearer server-token", seenAuth)
	}
}
