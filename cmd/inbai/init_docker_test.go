// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type composeCall struct {
	dir  string
	env  []string
	args []string
	out  io.Writer
}

func TestInitDockerModeFullFlowAndIdempotency(t *testing.T) {
	var (
		keys             int
		recoveryPassword string
		calls            []composeCall
	)

	recoverySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass == "" || pass != recoveryPassword {
			t.Errorf("basic auth ok=%v user=%q passMatch=%v", ok, user, pass == recoveryPassword)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
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
			t.Errorf("unexpected method %s", name)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer recoverySrv.Close()

	httpsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer httpsSrv.Close()

	origRun := runCompose
	t.Cleanup(func() { runCompose = origRun })
	runCompose = func(_ context.Context, dir string, env []string, out io.Writer, args ...string) ([]byte, error) {
		calls = append(calls, composeCall{dir: dir, env: append([]string(nil), env...), args: append([]string(nil), args...), out: out})
		for _, kv := range env {
			if strings.HasPrefix(kv, "STALWART_RECOVERY_ADMIN=admin:") {
				recoveryPassword = strings.TrimPrefix(kv, "STALWART_RECOVERY_ADMIN=admin:")
			}
		}
		return []byte("ok"), nil
	}

	recPort := mustPort(t, recoverySrv.URL)
	tlsPort := mustPort(t, httpsSrv.URL)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"init",
		"--domain", "example.com",
		"--config", cfgPath,
		"--recovery-port", recPort,
		"--https-port", tlsPort,
	}, &stdout, &stderr, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("exit %d stdout %s stderr %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "inbaid serve") {
		t.Fatalf("stdout missing next step: %s", stdout.String())
	}
	if len(calls) != 4 {
		t.Fatalf("expected 4 compose calls (version, pull, up recovery, up normal), got %d: %+v", len(calls), calls)
	}
	if strings.Join(calls[1].args, " ") != "-f compose.yaml pull --policy missing" || calls[1].out != io.Writer(&stderr) {
		t.Fatalf("second compose call should pull images with progress on stderr: %+v", calls[1])
	}
	if !containsEnv(calls[2].env, "STALWART_RECOVERY_MODE=1") {
		t.Fatalf("third compose call should enable recovery mode: %+v", calls[2].env)
	}
	if !containsEnv(calls[3].env, "STALWART_RECOVERY_MODE=0") || !containsEnv(calls[3].env, "STALWART_RECOVERY_ADMIN=") {
		t.Fatalf("fourth compose call should leave recovery mode: %+v", calls[3].env)
	}

	stackDir := filepath.Join(dir, "stack")
	for _, rel := range []string{"compose.yaml", "entrypoint.sh", filepath.Join("initdb", "01-inbai.sql"), ".env"} {
		if _, err := os.Stat(filepath.Join(stackDir, rel)); err != nil {
			t.Fatalf("missing stack file %s: %v", rel, err)
		}
	}
	envPath := filepath.Join(stackDir, ".env")
	envRaw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(envRaw), "STALWART_RECOVERY_ADMIN") || strings.Contains(string(envRaw), recoveryPassword) {
		t.Fatalf(".env must not persist recovery credentials: %s", envRaw)
	}
	if !strings.Contains(string(envRaw), "STALWART_RECOVERY_MODE=0") {
		t.Fatalf(".env should default STALWART_RECOVERY_MODE=0: %s", envRaw)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(envPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf(".env mode %o", info.Mode().Perm())
		}
	}

	pgPass := readEnvValue(envPath, "PG_PASSWORD")
	if pgPass == "" {
		t.Fatalf("empty PG_PASSWORD in %s", envRaw)
	}

	cfgRaw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["domain"] != "example.com" || cfg["apiKey"] != "shown-once" || cfg["stackDir"] != stackDir {
		t.Fatalf("config %s", cfgRaw)
	}
	if cfg["stalwartUrl"] != "https://127.0.0.1:"+tlsPort {
		t.Fatalf("stalwartUrl %v", cfg["stalwartUrl"])
	}
	dbURL, _ := cfg["databaseUrl"].(string)
	if !strings.Contains(dbURL, pgPass) || !strings.Contains(dbURL, ":5433/inbai") {
		t.Fatalf("databaseUrl %q does not match PG_PASSWORD/port", dbURL)
	}
	opToken, _ := cfg["operatorToken"].(string)
	if !strings.HasPrefix(opToken, "inb_") {
		t.Fatalf("operatorToken %q", opToken)
	}

	// Second run must keep PG_PASSWORD, APIKey, and OperatorToken intact and emit clean JSON.
	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"init",
		"--json",
		"--domain", "example.com",
		"--config", cfgPath,
		"--recovery-port", recPort,
		"--https-port", tlsPort,
	}, &stdout, &stderr, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("rerun exit %d stderr %s", code, stderr.String())
	}
	if readEnvValue(envPath, "PG_PASSWORD") != pgPass {
		t.Fatalf("PG_PASSWORD changed across rerun")
	}
	cfgAgainRaw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfgAgain map[string]any
	if err := json.Unmarshal(cfgAgainRaw, &cfgAgain); err != nil {
		t.Fatal(err)
	}
	if cfgAgain["apiKey"] != cfg["apiKey"] || cfgAgain["operatorToken"] != cfg["operatorToken"] {
		t.Fatalf("secrets rotated across rerun: before=%s after=%s", cfgRaw, cfgAgainRaw)
	}
	var printed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &printed); err != nil {
		t.Fatalf("invalid json %q: %v", stdout.String(), err)
	}
	if printed["stackDir"] != stackDir || printed["stalwartUrl"] != "https://127.0.0.1:"+tlsPort {
		t.Fatalf("printed %s", stdout.String())
	}
	for _, secret := range []string{"shown-once", opToken, pgPass, "apiKey", "operatorToken", "databaseUrl"} {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("json output leaked %q: %s", secret, stdout.String())
		}
	}
}

func TestInitDockerPublicBindsStandardPorts(t *testing.T) {
	recoverySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			writeInit(w, name, "k", map[string]any{"created": map[string]any{"k1": map[string]any{"secret": "shown-once"}}})
		}
	}))
	defer recoverySrv.Close()

	origRun := runCompose
	origWait := waitForURL
	t.Cleanup(func() {
		runCompose = origRun
		waitForURL = origWait
	})
	runCompose = func(_ context.Context, _ string, _ []string, _ io.Writer, _ ...string) ([]byte, error) {
		return []byte("ok"), nil
	}
	waitForURL = func(_ context.Context, _ string, _ bool) error { return nil }

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"init",
		"--public",
		"--domain", "mail.example.com",
		"--config", cfgPath,
		"--recovery-port", mustPort(t, recoverySrv.URL),
	}, &stdout, &stderr, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	envPath := filepath.Join(dir, "stack", ".env")
	if got := readEnvValue(envPath, "SMTP_BIND"); got != "25" {
		t.Fatalf("SMTP_BIND=%q", got)
	}
	assertNoSubmissionPort(t, filepath.Join(dir, "stack"))
	if got := readEnvValue(envPath, "HTTPS_BIND"); got != "443" {
		t.Fatalf("HTTPS_BIND=%q", got)
	}
	cfg, err := readConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StalwartURL != "https://127.0.0.1" {
		t.Fatalf("StalwartURL=%q", cfg.StalwartURL)
	}
}

// assertNoSubmissionPort fails when the stack in dir publishes SMTP submission.
// inbAI agents send through the API, so only 25 and 443 face the network.
func assertNoSubmissionPort(t *testing.T, dir string) {
	t.Helper()
	compose, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(compose), "\n") {
		if strings.Contains(line, ":587") || strings.Contains(line, "SUBMISSION") {
			t.Errorf("compose.yaml publishes submission: %s", strings.TrimSpace(line))
		}
	}
	for _, line := range envLines(t, dir) {
		if strings.Contains(line, "SUBMISSION") || publishesPort(line, "587") {
			t.Errorf(".env has a submission port: %s", line)
		}
	}
}

// envLines returns the lines of the stack's .env file.
func envLines(t *testing.T, dir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// publishesPort reports whether an .env line's value is port, alone or after
// an address. A random password that contains the digits does not count.
func publishesPort(line, port string) bool {
	_, value, _ := strings.Cut(line, "=")
	return value == port || strings.HasSuffix(value, ":"+port)
}

func TestInitDockerStackPublishesNoSubmissionPort(t *testing.T) {
	for name, extra := range map[string][]string{"local": nil, "public": {"--public"}} {
		t.Run(name, func(t *testing.T) {
			recovery := fakeRecovery(t, &recoveryState{keySecret: "shown-once"})
			https := fakeHTTPS(t, func(string) int { return http.StatusOK }, nil)
			fakeCompose(t, "")
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.json")
			var stdout, stderr bytes.Buffer
			if code := run(dockerInitArgs(cfgPath, recovery, https, extra...), &stdout, &stderr, noEnv); code != 0 {
				t.Fatalf("exit %d stderr %s", code, stderr.String())
			}
			stack := filepath.Join(dir, "stack")
			assertNoSubmissionPort(t, stack)
			compose, err := os.ReadFile(filepath.Join(stack, "compose.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"${SMTP_BIND:-127.0.0.1:2525}:25", "${HTTPS_BIND:-127.0.0.1:8443}:443"} {
				if !strings.Contains(string(compose), want) {
					t.Errorf("compose.yaml lacks %s", want)
				}
			}
			if strings.Contains(stderr.String(), "submission") {
				t.Errorf("init mentioned submission without the flag: %s", stderr.String())
			}
		})
	}
}

func TestInitDockerIgnoresSubmissionPort(t *testing.T) {
	recovery := fakeRecovery(t, &recoveryState{keySecret: "shown-once"})
	https := fakeHTTPS(t, func(string) int { return http.StatusOK }, nil)
	fakeCompose(t, "")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	var stdout, stderr bytes.Buffer
	if code := run(dockerInitArgs(cfgPath, recovery, https, "--submission-port", "15870"), &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if got := strings.Count(stderr.String(), submissionPortNotice); got != 1 {
		t.Fatalf("notice printed %d times: %s", got, stderr.String())
	}
	stack := filepath.Join(dir, "stack")
	assertNoSubmissionPort(t, stack)
	for _, line := range envLines(t, stack) {
		if publishesPort(line, "15870") {
			t.Fatalf("the ignored port reached .env: %s", line)
		}
	}
}

func TestInitDockerMissingDockerGivesClearError(t *testing.T) {
	origRun := runCompose
	t.Cleanup(func() { runCompose = origRun })
	runCompose = func(_ context.Context, _ string, _ []string, _ io.Writer, _ ...string) ([]byte, error) {
		return nil, errors.New("executable file not found in $PATH")
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"init", "--domain", "example.com", "--config", filepath.Join(t.TempDir(), "config.json")}, &stdout, &stderr, func(string) string { return "" })
	if code != 1 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "docker compose") || !strings.Contains(stderr.String(), "INBAI_RECOVERY_PASSWORD") {
		t.Fatalf("stderr %s", stderr.String())
	}
}

func TestInitDockerLeavesRecoveryOnBootstrapError(t *testing.T) {
	recoverySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer recoverySrv.Close()

	var calls []composeCall
	origRun := runCompose
	origWait := waitForURL
	t.Cleanup(func() {
		runCompose = origRun
		waitForURL = origWait
	})
	runCompose = func(_ context.Context, dir string, env []string, out io.Writer, args ...string) ([]byte, error) {
		calls = append(calls, composeCall{dir: dir, env: append([]string(nil), env...), args: append([]string(nil), args...), out: out})
		return []byte("ok"), nil
	}
	waitForURL = func(_ context.Context, _ string, _ bool) error { return nil }

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"init",
		"--domain", "example.com",
		"--config", filepath.Join(t.TempDir(), "config.json"),
		"--recovery-port", mustPort(t, recoverySrv.URL),
	}, &stdout, &stderr, func(string) string { return "" })
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if len(calls) != 4 || !containsEnv(calls[3].env, "STALWART_RECOVERY_MODE=0") {
		t.Fatalf("expected recovery cleanup call, got %+v", calls)
	}
}

func mustPort(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Port()
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}
