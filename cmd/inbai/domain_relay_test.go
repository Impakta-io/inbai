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
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/config"
)

func TestDomainRelayUsesTheOperatorAndHidesThePassword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "operator@example.com" || pass != "operator-secret" {
			t.Errorf("basic auth %q %q", user, pass)
		}
		if r.Header.Get("Authorization") == "Bearer shown-once" {
			t.Error("relay used the service API key")
		}
		var req struct {
			MethodCalls [][]json.RawMessage `json:"methodCalls"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		responses := make([]any, 0, len(req.MethodCalls))
		for _, call := range req.MethodCalls {
			var name, id string
			if err := json.Unmarshal(call[0], &name); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(call[2], &id); err != nil {
				t.Fatal(err)
			}
			var payload any
			switch name {
			case "x:MtaRoute/query":
				payload = map[string]any{"ids": []any{}}
			case "x:MtaRoute/get":
				payload = map[string]any{"list": []any{}}
			case "x:MtaRoute/set":
				payload = map[string]any{"created": map[string]any{"sm": map[string]any{"id": "route-1"}}}
			case "x:MtaOutboundStrategy/set":
				payload = map[string]any{"updated": map[string]any{"singleton": nil}}
			case "x:Action/ReloadSettings/set":
				payload = map[string]any{"created": map[string]any{"a": nil}}
			default:
				t.Errorf("method %s", name)
			}
			responses = append(responses, []any{name, payload, id})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"methodResponses": responses})
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "config.json")
	raw := []byte(`{"domain":"example.com","stalwartUrl":"` + srv.URL + `","apiKey":"shown-once"}` + "\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(key string) string {
		switch key {
		case "INBAI_OPERATOR_PASSWORD":
			return "operator-secret"
		case "INBAI_RELAY_PASSWORD":
			return "relay-secret"
		default:
			return ""
		}
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"domain", "relay", "--host", "smtp.example.com", "--user", "mailer", "--config", path}, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "relay-secret") || strings.Contains(stderr.String(), "relay-secret") {
		t.Fatalf("password leaked stdout %s stderr %s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "smtp.example.com:587 as mailer") {
		t.Fatalf("stdout %s", stdout.String())
	}

	stdout.Reset()
	code = run([]string{"domain", "relay", "--json", "--host", "smtp.example.com", "--user", "mailer", "--config", path}, &stdout, &stderr, env)
	if code != 0 {
		t.Fatalf("json exit %d stderr %s", code, stderr.String())
	}
	var printed struct {
		Mode string `json:"mode"`
		Host string `json:"host"`
		User string `json:"user"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &printed); err != nil {
		t.Fatalf("json %s", stdout.String())
	}
	if printed.Mode != "relay" || printed.Host != "smtp.example.com" || printed.User != "mailer" || strings.Contains(stdout.String(), "relay-secret") {
		t.Fatalf("printed %+v %s", printed, stdout.String())
	}
}

func TestDomainRelayRequiresThePasswords(t *testing.T) {
	var stdout, stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "config.json")
	code := run([]string{"domain", "relay", "--host", "smtp.example.com", "--user", "mailer", "--config", path}, &stdout, &stderr, func(string) string { return "" })
	if code != 2 || !strings.Contains(stderr.String(), "INBAI_OPERATOR_PASSWORD") {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
}

func TestDomainRelayPrefersTheEnvironmentOverTheStoredPassword(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want string
	}{
		{"stored", "", "stored-secret"},
		{"environment", "env-secret", "env-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			srv := fakeHTTPS(t, func(string) int { return http.StatusOK }, func(_, pass string) { got = pass })
			path := filepath.Join(t.TempDir(), "config.json")
			writeStored(t, path, storedConfig{
				File:             config.File{Domain: "example.com", StalwartURL: srv.URL},
				OperatorPassword: "stored-secret",
			})
			env := func(key string) string {
				switch key {
				case operatorPasswordEnv:
					return tc.env
				case relayPasswordEnv:
					return "relay-secret"
				}
				return ""
			}
			var stdout, stderr bytes.Buffer
			if code := run([]string{"domain", "relay", "--host", "smtp.example.net", "--user", "mailer", "--config", path}, &stdout, &stderr, env); code != 0 {
				t.Fatalf("exit %d stderr %s", code, stderr.String())
			}
			if got != tc.want {
				t.Fatalf("signed in with %q, want %q", got, tc.want)
			}
			if strings.Contains(stdout.String()+stderr.String(), "stored-secret") {
				t.Fatalf("printed the stored password: %s %s", stdout.String(), stderr.String())
			}
		})
	}
}
