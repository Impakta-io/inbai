// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOneInboxOnStalwart(t *testing.T) {
	if os.Getenv("INBAI_INTEGRATION") != "1" {
		t.Skip("INBAI_INTEGRATION=1 starts the Compose stack and proves one inbox")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker is required")
	}
	root := repoRoot(t)
	bin := t.TempDir()
	build(t, root, bin)
	cfgDir := t.TempDir()
	cfg := filepath.Join(cfgDir, "config.json")
	password := secret()
	compose := filepath.Join(root, "deploy", "compose")
	env := []string{
		"PG_PASSWORD=" + password,
		"STALWART_RECOVERY_MODE=1",
		"STALWART_RECOVERY_ADMIN=admin:" + password,
		"INBAI_OPERATOR_TOKEN=integration-token",
		"INBAI_CONFIG_DIR=" + cfgDir,
	}
	t.Cleanup(func() {
		_, _ = command(t, compose, env, "docker", "compose", "-p", "inbai-live", "down", "-v", "--remove-orphans")
	})
	_, _ = command(t, compose, env, "docker", "compose", "-p", "inbai-live", "down", "-v", "--remove-orphans")
	must(t, compose, env, "docker", "compose", "-p", "inbai-live", "up", "-d")
	waitURL(t, "http://127.0.0.1:8080/jmap/", false)

	must(t, root, []string{"INBAI_RECOVERY_PASSWORD=" + password},
		filepath.Join(bin, "inbai"), "init",
		"--url", "http://127.0.0.1:8080",
		"--domain", "inbai.test",
		"--config", cfg,
	)

	env[1] = "STALWART_RECOVERY_MODE=0"
	must(t, compose, env, "docker", "compose", "-p", "inbai-live", "up", "-d", "--force-recreate", "stalwart")
	waitURL(t, "https://127.0.0.1/jmap/", true)
	waitURL(t, "http://127.0.0.1:8787/healthz", false)

	cli := []string{"INBAI_OPERATOR_TOKEN=integration-token"}
	must(t, root, cli, filepath.Join(bin, "inbai"), "inbox", "create", "--config", cfg, "alice")
	must(t, root, cli, filepath.Join(bin, "inbai"), "message", "send", "--config", cfg,
		"--from", "alice@inbai.test", "--to", "alice@inbai.test", "--subject", "hello", "--body", "hello from inbai")

	var listed struct {
		Messages []struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
		} `json:"messages"`
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		out := must(t, root, cli, filepath.Join(bin, "inbai"), "message", "list", "--config", cfg, "--json", "alice@inbai.test")
		if err := json.Unmarshal([]byte(out), &listed); err != nil {
			t.Fatalf("list %s: %v", out, err)
		}
		if len(listed.Messages) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inbox stayed empty")
		}
		time.Sleep(time.Second)
	}
	id := ""
	for _, message := range listed.Messages {
		if message.Subject == "hello" {
			id = message.ID
		}
	}
	if id == "" {
		t.Fatalf("subject hello was not listed: %+v", listed.Messages)
	}
	got := must(t, root, cli, filepath.Join(bin, "inbai"), "message", "get", "--config", cfg, "--json", "alice@inbai.test", id)
	if !strings.Contains(got, "hello from inbai") {
		t.Fatalf("message %s", got)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func build(t *testing.T, root, bin string) {
	t.Helper()
	must(t, root, nil, "go", "build", "-o", filepath.Join(bin, "inbai"), "./cmd/inbai")
	must(t, root, nil, "go", "build", "-o", filepath.Join(bin, "inbaid"), "./cmd/inbaid")
}

func must(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	out, err := command(t, dir, env, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, tail(out))
	}
	return out
}

func command(t *testing.T, dir string, env []string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	raw, err := cmd.CombinedOutput()
	return string(raw), err
}

func waitURL(t *testing.T, raw string, insecure bool) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	if insecure {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // the local Stalwart certificate is self-signed
		}
	}
	deadline := time.Now().Add(2 * time.Minute)
	var last error
	for time.Now().Before(deadline) {
		res, err := client.Get(raw)
		if err == nil {
			_ = res.Body.Close()
			return
		}
		last = err
		time.Sleep(time.Second)
	}
	t.Fatalf("timed out waiting for %s: %v", raw, last)
}

func secret() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func tail(text string) string {
	if len(text) > 2000 {
		return text[len(text)-2000:]
	}
	return text
}
