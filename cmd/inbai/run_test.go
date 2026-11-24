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
	"runtime"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/config"
	"github.com/Impakta-io/inbai/internal/version"
)

func TestRunPrintsVersionWithoutArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if stdout.String() != version.String(version.CLI)+"\n" {
		t.Fatalf("stdout %q", stdout.String())
	}
}

func TestInitRequiresRecoveryPassword(t *testing.T) {
	for _, args := range [][]string{
		{"init", "--no-docker", "--domain", "example.com"},
		{"init", "--url", "http://127.0.0.1:8080", "--domain", "example.com"},
	} {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr, func(string) string { return "" })
		if code != 2 {
			t.Fatalf("args %v exit %d stdout %s stderr %s", args, code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "INBAI_RECOVERY_PASSWORD") {
			t.Fatalf("args %v stderr %s", args, stderr.String())
		}
	}
}

func TestInitWritesConfigAndPrintsRetention(t *testing.T) {
	var keys int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := methodName(t, r.Body)
		switch name {
		case "x:Domain/set":
			writeInit(w, name, "d", map[string]any{"created": map[string]any{"d1": map[string]any{"id": "dom"}}})
		case "x:SystemSettings/set":
			writeInit(w, name, "s", map[string]any{"updated": map[string]any{}})
		case "x:Role/query":
			writeInit(w, name, "q", map[string]any{"ids": []string{}})
		case "x:Role/set":
			writeInit(w, name, "r", map[string]any{"created": map[string]any{
				"inbox": map[string]any{"id": "inbox-role"},
				"svc":   map[string]any{"id": "service-role"},
				"op":    map[string]any{"id": "operator-role"},
			}})
		case "x:Account/set":
			writeInit(w, name, "a", map[string]any{"created": map[string]any{
				"operator": map[string]any{"id": "op"},
				"svc":      map[string]any{"id": "svc-id"},
			}})
		case "x:ApiKey/set":
			keys++
			if keys > 1 {
				t.Errorf("second init created another API key")
			}
			writeInit(w, name, "k", map[string]any{"created": map[string]any{"k1": map[string]any{"secret": "shown-once"}}})
		default:
			t.Errorf("method %s", name)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	var stdout, stderr bytes.Buffer
	code := run([]string{"init", "--url", srv.URL, "--domain", "example.com", "--config", path}, &stdout, &stderr, func(key string) string {
		if key == "INBAI_RECOVERY_PASSWORD" {
			return "recovery-secret"
		}
		return ""
	})
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "30 days") {
		t.Fatalf("stdout %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "operator@example.com") {
		t.Fatalf("stdout %s", stdout.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["domain"] != "example.com" || cfg["apiKey"] == "" || cfg["retentionDays"] != float64(30) {
		t.Fatalf("config %s", raw)
	}
	opToken, _ := cfg["operatorToken"].(string)
	if !strings.HasPrefix(opToken, "inb_") || config.CheckOperatorToken(opToken) != nil {
		t.Fatalf("operatorToken %q", opToken)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %o", info.Mode().Perm())
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"init", "--url", srv.URL, "--domain", "example.com", "--config", path}, &stdout, &stderr, func(key string) string {
		if key == "INBAI_RECOVERY_PASSWORD" {
			return "recovery-secret"
		}
		return ""
	})
	if code != 0 {
		t.Fatalf("rerun exit %d stderr %s", code, stderr.String())
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept map[string]any
	if err := json.Unmarshal(again, &kept); err != nil {
		t.Fatal(err)
	}
	if kept["apiKey"] != cfg["apiKey"] {
		t.Fatalf("api key changed from %v to %v", cfg["apiKey"], kept["apiKey"])
	}
	if kept["operatorToken"] != cfg["operatorToken"] {
		t.Fatalf("operator token changed from %v to %v", cfg["operatorToken"], kept["operatorToken"])
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"init", "--json", "--url", srv.URL, "--domain", "example.com", "--config", path}, &stdout, &stderr, func(key string) string {
		if key == "INBAI_RECOVERY_PASSWORD" {
			return "recovery-secret"
		}
		return ""
	})
	if code != 0 {
		t.Fatalf("json exit %d stderr %s", code, stderr.String())
	}
	var printed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &printed); err != nil {
		t.Fatalf("json %s", stdout.String())
	}
	if printed["domain"] != "example.com" || printed["retentionDays"] != float64(30) || printed["operator"] != "operator@example.com" {
		t.Fatalf("printed %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "shown-once") || strings.Contains(stdout.String(), "apiKey") || strings.Contains(stdout.String(), opToken) || strings.Contains(stdout.String(), "operatorToken") {
		t.Fatalf("json included secret material: %s", stdout.String())
	}
}

func methodName(t *testing.T, body io.Reader) string {
	t.Helper()
	var req struct {
		MethodCalls [][]json.RawMessage `json:"methodCalls"`
	}
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := json.Unmarshal(req.MethodCalls[0][0], &name); err != nil {
		t.Fatal(err)
	}
	return name
}

func writeInit(w http.ResponseWriter, name, id string, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"methodResponses": []any{[]any{name, payload, id}},
	})
}

func TestInitRefusesAShortOperatorToken(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected", http.StatusBadRequest)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"init", "--url", srv.URL, "--domain", "example.com", "--config", path}, &stdout, &stderr, func(key string) string {
		switch key {
		case "INBAI_RECOVERY_PASSWORD":
			return "recovery-secret"
		case "INBAI_OPERATOR_TOKEN":
			return "short"
		}
		return ""
	})
	if code != 2 || !strings.Contains(stderr.String(), "minimum is 32") {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if requests != 0 {
		t.Fatalf("init changed Stalwart before refusing the token")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a refused token was written to the config")
	}
}

func TestGeneratedOperatorTokenMeetsTheServerMinimum(t *testing.T) {
	token, err := generateOperatorToken()
	if err != nil || config.CheckOperatorToken(token) != nil {
		t.Fatalf("token %q err %v", token, err)
	}
}
