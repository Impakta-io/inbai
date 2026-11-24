// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
)

// recoveryState is what the fake recovery API saw and how it answers.
type recoveryState struct {
	mu             sync.Mutex
	accountsExist  bool
	keySecret      string
	apiKeys        int
	operatorCreate map[string]any
	operatorUpdate map[string]any
}

// operatorSecret is the password the fake recovery API was last asked to set
// on the operator, by create or by update.
func (s *recoveryState) operatorSecret() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, body := range []map[string]any{s.operatorUpdate, s.operatorCreate} {
		creds, _ := body["credentials"].(map[string]any)
		first, _ := creds["0"].(map[string]any)
		if secret, _ := first["secret"].(string); secret != "" {
			return secret
		}
	}
	return ""
}

func fakeRecovery(t *testing.T, st *recoveryState) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if user, _, ok := r.BasicAuth(); !ok || user != "admin" {
			t.Errorf("recovery basic auth user %q ok=%v", user, ok)
		}
		var req struct {
			MethodCalls [][]json.RawMessage `json:"methodCalls"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		var name string
		var args map[string]any
		_ = json.Unmarshal(req.MethodCalls[0][0], &name)
		_ = json.Unmarshal(req.MethodCalls[0][1], &args)
		st.mu.Lock()
		defer st.mu.Unlock()
		switch name {
		case "x:Domain/set":
			writeInit(w, name, "d", map[string]any{"created": map[string]any{"d1": map[string]any{"id": "dom"}}})
		case "x:SystemSettings/set":
			writeInit(w, name, "s", map[string]any{"updated": map[string]any{}})
		case "x:MtaStageData/set":
			writeInit(w, name, "m", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:Role/query":
			writeInit(w, name, "q", map[string]any{"ids": []string{}})
		case "x:Role/set":
			writeInit(w, name, "r", map[string]any{"created": map[string]any{
				"inbox": map[string]any{"id": "inbox-role"},
				"svc":   map[string]any{"id": "service-role"},
				"op":    map[string]any{"id": "operator-role"},
			}})
		case "x:Account/set":
			if update, ok := args["update"].(map[string]any); ok {
				st.operatorUpdate, _ = update["op"].(map[string]any)
				writeInit(w, name, "u", map[string]any{"updated": map[string]any{"op": nil}})
				return
			}
			create := args["create"].(map[string]any)
			st.operatorCreate, _ = create["operator"].(map[string]any)
			if st.accountsExist {
				writeInit(w, name, "a", map[string]any{"notCreated": map[string]any{
					"operator": map[string]any{"type": "alreadyExists", "objectId": map[string]any{"id": "op"}},
					"svc":      map[string]any{"type": "alreadyExists", "objectId": map[string]any{"id": "svc-id"}},
				}})
				return
			}
			writeInit(w, name, "a", map[string]any{"created": map[string]any{
				"operator": map[string]any{"id": "op"},
				"svc":      map[string]any{"id": "svc-id"},
			}})
		case "x:ApiKey/set":
			st.apiKeys++
			if args["accountId"] != "svc-id" {
				t.Errorf("api key for %v", args["accountId"])
			}
			writeInit(w, name, "k", map[string]any{"created": map[string]any{"k1": map[string]any{"secret": st.keySecret}}})
		default:
			t.Errorf("unexpected method %s", name)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeHTTPS is Stalwart in normal mode. A bearer token is the API key check.
// Basic auth is domain relay signing in as the operator.
func fakeHTTPS(t *testing.T, keyStatus func(key string) int, onBasic func(user, pass string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		if key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			status := keyStatus(key)
			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			writeInit(w, "Core/echo", "e", map[string]any{})
			return
		}
		user, pass, _ := r.BasicAuth()
		if onBasic != nil {
			onBasic(user, pass)
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
			_ = json.Unmarshal(call[0], &name)
			_ = json.Unmarshal(call[2], &id)
			var payload any = map[string]any{}
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
			}
			responses = append(responses, []any{name, payload, id})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"methodResponses": responses})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeCompose records docker compose calls and answers "logs" with logs.
func fakeCompose(t *testing.T, logs string) *[]composeCall {
	t.Helper()
	var (
		mu    sync.Mutex
		calls []composeCall
	)
	origRun, origWait := runCompose, waitForURL
	t.Cleanup(func() { runCompose, waitForURL = origRun, origWait })
	runCompose = func(_ context.Context, dir string, env []string, out io.Writer, args ...string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, composeCall{dir: dir, env: append([]string(nil), env...), args: append([]string(nil), args...), out: out})
		if len(args) > 2 && args[2] == "logs" {
			return []byte(logs), nil
		}
		return []byte("ok"), nil
	}
	return &calls
}

func dockerInitArgs(cfgPath string, recovery, https *httptest.Server, extra ...string) []string {
	return append([]string{
		"init",
		"--domain", "example.com",
		"--config", cfgPath,
		"--recovery-port", portOf(recovery.URL),
		"--https-port", portOf(https.URL),
	}, extra...)
}

func portOf(rawURL string) string {
	return rawURL[strings.LastIndex(rawURL, ":")+1:]
}

func writeStored(t *testing.T, path string, cfg storedConfig) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func noEnv(string) string { return "" }

func TestInitDockerStoresOperatorPasswordForDomainRelay(t *testing.T) {
	st := &recoveryState{keySecret: "shown-once"}
	recovery := fakeRecovery(t, st)
	var relayUser, relayPass string
	https := fakeHTTPS(t, func(string) int { return http.StatusOK }, func(user, pass string) {
		relayUser, relayPass = user, pass
	})
	calls := fakeCompose(t, "")
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	var stdout, stderr bytes.Buffer
	if code := run(dockerInitArgs(cfgPath, recovery, https), &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	stored, err := readStoredConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.OperatorPassword == "" || stored.OperatorPassword != st.operatorSecret() {
		t.Fatalf("stored operator password %q, Stalwart was given %q", stored.OperatorPassword, st.operatorSecret())
	}
	for _, call := range *calls {
		for _, kv := range call.env {
			if strings.Contains(kv, stored.OperatorPassword) {
				t.Fatalf("operator password is the recovery password: %s", kv)
			}
		}
	}
	if strings.Contains(stdout.String()+stderr.String(), stored.OperatorPassword) {
		t.Fatalf("init printed the operator password: %s %s", stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(dockerInitArgs(cfgPath, recovery, https, "--json"), &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("rerun exit %d stderr %s", code, stderr.String())
	}
	again, err := readStoredConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if again.OperatorPassword != stored.OperatorPassword {
		t.Fatal("rerun changed the stored operator password")
	}
	if st.operatorUpdate["credentials"] != nil {
		t.Fatalf("rerun reset a password the config already had: %v", st.operatorUpdate)
	}
	if strings.Contains(stdout.String(), stored.OperatorPassword) || strings.Contains(stdout.String(), "operatorPassword") {
		t.Fatalf("json printed the operator password: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	relayEnv := func(key string) string {
		if key == relayPasswordEnv {
			return "relay-secret"
		}
		return ""
	}
	if code := run([]string{"domain", "relay", "--host", "smtp.example.net", "--user", "mailer", "--config", cfgPath}, &stdout, &stderr, relayEnv); code != 0 {
		t.Fatalf("domain relay exit %d stderr %s", code, stderr.String())
	}
	if relayUser != "operator@example.com" || relayPass != stored.OperatorPassword {
		t.Fatalf("domain relay signed in as %q with the wrong password", relayUser)
	}
}

func TestInitDockerRerunSetsOperatorPasswordOnOlderInstall(t *testing.T) {
	st := &recoveryState{accountsExist: true}
	recovery := fakeRecovery(t, st)
	https := fakeHTTPS(t, func(key string) int {
		if key != "kept-key" {
			t.Errorf("checked key %q", key)
		}
		return http.StatusOK
	}, nil)
	fakeCompose(t, "")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeStored(t, cfgPath, storedConfig{File: config.File{
		Domain:        "example.com",
		APIKey:        "kept-key",
		StackDir:      filepath.Join(dir, "stack"),
		RetentionDays: 7,
	}})

	var stdout, stderr bytes.Buffer
	if code := run(dockerInitArgs(cfgPath, recovery, https), &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	stored, err := readStoredConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.operatorUpdate == nil || st.operatorUpdate["credentials"] == nil {
		t.Fatalf("existing operator did not get a password: %v", st.operatorUpdate)
	}
	if stored.OperatorPassword == "" || stored.OperatorPassword != st.operatorSecret() {
		t.Fatalf("stored %q, set %q", stored.OperatorPassword, st.operatorSecret())
	}
	if stored.APIKey != "kept-key" || st.apiKeys != 0 {
		t.Fatalf("api key %q, keys issued %d", stored.APIKey, st.apiKeys)
	}
	if stored.RetentionDays != 7 {
		t.Fatalf("retention %d", stored.RetentionDays)
	}
}

func TestInitDockerReplacesAPIKeyStalwartRejects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		wantKey string
		wantMsg string
	}{
		{"rejected", http.StatusUnauthorized, "fresh-key", "rejected the stored API key"},
		{"unchecked", http.StatusInternalServerError, "stale-key", "Could not check the stored API key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &recoveryState{keySecret: "fresh-key"}
			recovery := fakeRecovery(t, st)
			https := fakeHTTPS(t, func(key string) int {
				if key == "fresh-key" {
					return http.StatusOK
				}
				return tc.status
			}, nil)
			calls := fakeCompose(t, "")
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.json")
			writeStored(t, cfgPath, storedConfig{
				File: config.File{
					Domain:        "example.com",
					APIKey:        "stale-key",
					StackDir:      filepath.Join(dir, "stack"),
					RetentionDays: 30,
				},
				OperatorPassword: "op-secret",
			})

			var stdout, stderr bytes.Buffer
			if code := run(dockerInitArgs(cfgPath, recovery, https), &stdout, &stderr, noEnv); code != 0 {
				t.Fatalf("exit %d stderr %s", code, stderr.String())
			}
			stored, err := readStoredConfig(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if stored.APIKey != tc.wantKey {
				t.Fatalf("api key %q, want %q", stored.APIKey, tc.wantKey)
			}
			if !strings.Contains(stderr.String(), tc.wantMsg) {
				t.Fatalf("stderr %s", stderr.String())
			}
			if stored.OperatorPassword != "op-secret" || st.operatorSecret() != "op-secret" {
				t.Fatalf("operator password %q, Stalwart got %q", stored.OperatorPassword, st.operatorSecret())
			}
			last := (*calls)[len(*calls)-1]
			if !containsEnv(last.env, "STALWART_RECOVERY_MODE=0") {
				t.Fatalf("init ended in recovery mode: %+v", *calls)
			}
			if tc.status == http.StatusUnauthorized {
				if st.apiKeys != 1 || len(*calls) != 6 || !containsEnv((*calls)[4].env, "STALWART_RECOVERY_MODE=1") {
					t.Fatalf("keys %d calls %+v", st.apiKeys, *calls)
				}
			} else if st.apiKeys != 0 {
				t.Fatalf("issued a key without a rejection")
			}
		})
	}
}

func TestInitKeepsRetentionOnRerun(t *testing.T) {
	for _, days := range []int{0, 7} {
		t.Run(fmt.Sprint(days), func(t *testing.T) {
			recovery := fakeRecovery(t, &recoveryState{accountsExist: true})
			cfgPath := filepath.Join(t.TempDir(), "config.json")
			writeStored(t, cfgPath, storedConfig{File: config.File{Domain: "example.com", APIKey: "key", RetentionDays: days}})
			var stdout, stderr bytes.Buffer
			code := run([]string{"init", "--url", recovery.URL, "--domain", "example.com", "--config", cfgPath}, &stdout, &stderr, func(key string) string {
				if key == recoveryPasswordEnv {
					return "recovery-secret"
				}
				return ""
			})
			if code != 0 {
				t.Fatalf("exit %d stderr %s", code, stderr.String())
			}
			cfg, err := readConfig(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.RetentionDays != days {
				t.Fatalf("retention %d, want %d", cfg.RetentionDays, days)
			}
		})
	}
}

func TestInitCreatesConfigDirPrivate(t *testing.T) {
	recovery := fakeRecovery(t, &recoveryState{keySecret: "shown-once"})
	https := fakeHTTPS(t, func(string) int { return http.StatusOK }, nil)
	fakeCompose(t, "")
	cfgDir := filepath.Join(t.TempDir(), "new", "inbai")
	cfgPath := filepath.Join(cfgDir, "config.json")

	var stdout, stderr bytes.Buffer
	if code := run(dockerInitArgs(cfgPath, recovery, https), &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use Unix modes")
	}
	info, err := os.Stat(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("config dir mode %o", info.Mode().Perm())
	}
}

func TestInitLeavesAnExistingConfigDirMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use Unix modes")
	}
	recovery := fakeRecovery(t, &recoveryState{keySecret: "shown-once"})
	https := fakeHTTPS(t, func(string) int { return http.StatusOK }, nil)
	fakeCompose(t, "")
	cfgDir := filepath.Join(t.TempDir(), "mine")
	if err := os.Mkdir(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(dockerInitArgs(filepath.Join(cfgDir, "config.json"), recovery, https), &stdout, &stderr, noEnv); code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	info, err := os.Stat(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("init changed the mode of a directory it did not create: %o", info.Mode().Perm())
	}
}

// timeoutRun runs init against a Stalwart that never answers. Compose answers
// each service's logs and the stalwart ps call with the text given.
func timeoutRun(t *testing.T, stalwartLog, postgresLog, psJSON string) (stderr string, calls *[]composeCall, composePath string) {
	t.Helper()
	calls = fakeCompose(t, "")
	base := runCompose
	runCompose = func(ctx context.Context, dir string, env []string, out io.Writer, args ...string) ([]byte, error) {
		got, err := base(ctx, dir, env, out, args...)
		if len(args) > 2 && args[2] == "logs" {
			switch args[len(args)-1] {
			case "stalwart":
				return []byte(stalwartLog), err
			case "postgres":
				return []byte(postgresLog), err
			}
		}
		if len(args) > 2 && args[2] == "ps" {
			return []byte(psJSON), err
		}
		return got, err
	}
	origTimeout := readyTimeout
	t.Cleanup(func() { readyTimeout = origTimeout })
	readyTimeout = 50 * time.Millisecond
	waitForURL = func(ctx context.Context, _ string, _ bool) error {
		<-ctx.Done()
		return fmt.Errorf("%w (last error: connection refused)", ctx.Err())
	}
	dir := t.TempDir()
	var stdout, errBuf bytes.Buffer
	code := run([]string{"init", "--domain", "example.com", "--config", filepath.Join(dir, "config.json")}, &stdout, &errBuf, noEnv)
	if code != 1 {
		t.Fatalf("exit %d stderr %s", code, errBuf.String())
	}
	return errBuf.String(), calls, filepath.Join(dir, "stack", "compose.yaml")
}

func TestInitDockerTimeoutPermissionDenied(t *testing.T) {
	stderr, calls, _ := timeoutRun(t,
		"stalwart-1  | /entrypoint.sh: 4: cannot create /etc/stalwart/config.json: Permission denied\n",
		"postgres-1  | database system is ready to accept connections\n",
		`{"Service":"stalwart","State":"exited","ExitCode":2}`+"\n")
	for _, want := range []string{
		"Stalwart recovery API did not answer",
		"stalwart exited with code 2",
		"Permission denied",
		"not writable by the container user",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	for _, bad := range []string{"down -v", "different PostgreSQL password"} {
		if strings.Contains(stderr, bad) {
			t.Fatalf("stderr has %q:\n%s", bad, stderr)
		}
	}
	if strings.Index(stderr, "Permission denied") > strings.Index(stderr, "database system is ready") {
		t.Fatalf("the Stalwart log must come first:\n%s", stderr)
	}
	logsAt, leaveAt := -1, -1
	for i, call := range *calls {
		if len(call.args) > 2 && call.args[2] == "logs" && logsAt < 0 {
			logsAt = i
		}
		if containsEnv(call.env, "STALWART_RECOVERY_MODE=0") {
			leaveAt = i
		}
	}
	if logsAt < 0 || leaveAt < logsAt {
		t.Fatalf("logs must be read before Stalwart is restarted: %+v", *calls)
	}
}

func TestInitDockerTimeoutPasswordHint(t *testing.T) {
	stderr, _, composePath := timeoutRun(t,
		"stalwart-1  | waiting for database\n",
		"postgres-1  | FATAL:  password authentication failed for user \"stalwart\"\n",
		`[{"Service":"stalwart","State":"restarting","ExitCode":1}]`)
	for _, want := range []string{
		"stalwart is restarting",
		"password authentication failed",
		"Compose project inbai",
		"different PostgreSQL password",
		"deletes that install's mail and accounts",
		fmt.Sprintf("docker compose -p inbai -f %q down -v", composePath),
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "not writable") {
		t.Fatalf("wrong hint:\n%s", stderr)
	}
}

func TestInitDockerTimeoutGenericHint(t *testing.T) {
	stderr, _, _ := timeoutRun(t, "stalwart-1  | starting\n", "postgres-1  | ready\n", "")
	if !strings.Contains(stderr, "Stalwart did not answer in time; see the logs above.") {
		t.Fatalf("stderr:\n%s", stderr)
	}
	for _, bad := range []string{"down -v", "not writable", "exited"} {
		if strings.Contains(stderr, bad) {
			t.Fatalf("stderr has %q:\n%s", bad, stderr)
		}
	}
	stderr, _, _ = timeoutRun(t, "stalwart-1  | bye\n", "postgres-1  | ready\n", `{"State":"exited","ExitCode":1}`)
	if !strings.Contains(stderr, "Stalwart exited; see its log above.") || strings.Contains(stderr, "down -v") {
		t.Fatalf("stderr:\n%s", stderr)
	}
}

func TestWriteConfigLeavesTheOldFileWhenTheWriteFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := writeStoredConfig(path, storedConfig{File: config.File{Domain: "old.example.com"}, OperatorPassword: "kept"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	orig := syncConfigFile
	t.Cleanup(func() { syncConfigFile = orig })
	syncConfigFile = func(*os.File) error { return errors.New("disk full") }
	if err := writeConfig(path, config.File{Domain: "new.example.com"}); err == nil {
		t.Fatal("write succeeded")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("config changed:\n%s\n%s", before, after)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("left files behind: %v", entries)
	}

	syncConfigFile = orig
	if err := writeConfig(path, config.File{Domain: "new.example.com"}); err != nil {
		t.Fatal(err)
	}
	stored, err := readStoredConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Domain != "new.example.com" || stored.OperatorPassword != "kept" {
		t.Fatalf("stored %+v", stored)
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
}
