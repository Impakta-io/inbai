// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
	"github.com/Impakta-io/inbai/internal/version"
)

func TestRunPrintsVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr, func(string) string { return "" }); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if stdout.String() != version.String(version.Server)+"\n" {
		t.Fatalf("stdout %q", stdout.String())
	}
}

func TestServeRequiresOperatorToken(t *testing.T) {
	var stdout, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.json")
	code := run([]string{"serve", "--config", missing}, &stdout, &stderr, func(string) string { return "" })
	if code != 2 || !strings.Contains(stderr.String(), "INBAI_OPERATOR_TOKEN") {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
}

func TestServeReadsOperatorTokenFromConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw, err := json.Marshal(config.File{OperatorToken: "0123456789abcdef0123456789abcdef0123"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"serve", "--config", path}, &stdout, &stderr, func(string) string { return "" })
	// Token was read from config, so serve moves past the token check and fails on the missing service API key.
	if code != 2 || !strings.Contains(stderr.String(), "service API key") {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
}

func TestResolveTokenAndDatabaseURL(t *testing.T) {
	cfg := config.File{
		OperatorToken: "cfg-token",
		DatabaseURL:   "postgres://cfg/inbai",
	}
	if got := resolveToken(cfg, func(string) string { return "" }); got != "cfg-token" {
		t.Fatalf("token fallback %q", got)
	}
	if got := resolveToken(cfg, func(k string) string {
		if k == "INBAI_OPERATOR_TOKEN" {
			return "env-token"
		}
		return ""
	}); got != "env-token" {
		t.Fatalf("token override %q", got)
	}
	if got := resolveDatabaseURL(cfg, func(string) string { return "" }); got != "postgres://cfg/inbai" {
		t.Fatalf("dsn fallback %q", got)
	}
	if got := resolveDatabaseURL(cfg, func(k string) string {
		if k == "INBAI_DATABASE_URL" {
			return "postgres://env/inbai"
		}
		return ""
	}); got != "postgres://env/inbai" {
		t.Fatalf("dsn override %q", got)
	}
}

func TestIMAPServeRequiresDomainAndAccounts(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"serve"}, &stdout, &stderr, func(key string) string {
		switch key {
		case "INBAI_OPERATOR_TOKEN":
			return "0123456789abcdef0123456789abcdef0123"
		case "INBAI_MAIL":
			return "imap"
		default:
			return ""
		}
	})
	if code != 2 || !strings.Contains(stderr.String(), "INBAI_DOMAIN") {
		t.Fatalf("domain exit %d stderr %s", code, stderr.String())
	}
	stderr.Reset()
	code = run([]string{"serve"}, &stdout, &stderr, func(key string) string {
		switch key {
		case "INBAI_OPERATOR_TOKEN":
			return "0123456789abcdef0123456789abcdef0123"
		case "INBAI_MAIL":
			return "imap"
		case "INBAI_DOMAIN":
			return "example.com"
		default:
			return ""
		}
	})
	if code != 2 || !strings.Contains(stderr.String(), "account file") {
		t.Fatalf("accounts exit %d stderr %s", code, stderr.String())
	}
}

func TestHTTPServerBoundsSlowAndIdleConnections(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if server.Addr != "127.0.0.1:0" || server.Handler == nil {
		t.Fatalf("server %+v", server)
	}
	// The longest legitimate handler is a 25 second long poll or MCP wait.
	// Go cancels the request context when ReadTimeout passes mid-handler.
	longest := 25 * time.Second
	if server.ReadHeaderTimeout <= 0 || server.ReadHeaderTimeout > server.ReadTimeout {
		t.Fatalf("read header timeout %s, read timeout %s", server.ReadHeaderTimeout, server.ReadTimeout)
	}
	if server.ReadTimeout < 2*longest {
		t.Fatalf("read timeout %s would cut a long poll", server.ReadTimeout)
	}
	if server.WriteTimeout < server.ReadTimeout {
		t.Fatalf("write timeout %s is shorter than the read timeout %s", server.WriteTimeout, server.ReadTimeout)
	}
	if server.IdleTimeout <= 0 {
		t.Fatalf("idle timeout %s", server.IdleTimeout)
	}
}

func TestServeRefusesShortSecrets(t *testing.T) {
	strong := "0123456789abcdef0123456789abcdef0123"
	accounts := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(accounts, []byte(`{"ada":{"user":"ada@example.com","password":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.json")
	cases := []struct {
		name, mail, token, key, want string
	}{
		{"jmap short key", "", strong, "short-key", "INBAI_SIGNING_KEY"},
		{"jmap short token", "", "short-token", "", "INBAI_OPERATOR_TOKEN"},
		{"imap short key", "imap", strong, "short-key", "INBAI_SIGNING_KEY"},
		{"imap short token", "imap", "short-token", "", "INBAI_OPERATOR_TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := map[string]string{
				"INBAI_MAIL":           tc.mail,
				"INBAI_OPERATOR_TOKEN": tc.token,
				"INBAI_SIGNING_KEY":    tc.key,
				"INBAI_DOMAIN":         "example.com",
				"INBAI_IMAP_ADDRESS":   "127.0.0.1:1",
				"INBAI_SMTP_ADDRESS":   "127.0.0.1:1",
				"INBAI_IMAP_SECURITY":  "none",
				"INBAI_SMTP_SECURITY":  "none",
				"INBAI_IMAP_ACCOUNTS":  accounts,
			}
			code := run([]string{"serve", "--config", missing}, &stdout, &stderr, func(k string) string { return env[k] })
			if code != 2 || !strings.Contains(stderr.String(), tc.want) || !strings.Contains(stderr.String(), "minimum is 32") {
				t.Fatalf("exit %d stderr %s", code, stderr.String())
			}
		})
	}
}

func TestServeRefusesAShortTokenFromConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw, _ := json.Marshal(config.File{OperatorToken: "short"})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"serve", "--config", path}, &stdout, &stderr, func(string) string { return "" })
	if code != 2 || !strings.Contains(stderr.String(), "minimum is 32") {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
}
