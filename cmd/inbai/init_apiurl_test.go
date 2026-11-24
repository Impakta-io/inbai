// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/config"
)

func initWithRecovery(t *testing.T, cfgPath string, extra ...string) (int, string) {
	t.Helper()
	recovery := fakeRecovery(t, &recoveryState{accountsExist: true})
	args := append([]string{"init", "--url", recovery.URL, "--domain", "example.com", "--config", cfgPath}, extra...)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, func(key string) string {
		if key == recoveryPasswordEnv {
			return "recovery-secret"
		}
		return ""
	})
	return code, stdout.String() + stderr.String()
}

func TestInitWritesTheGivenAPIURL(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	writeStored(t, cfgPath, storedConfig{File: config.File{Domain: "example.com", APIKey: "key"}})
	code, out := initWithRecovery(t, cfgPath, "--api-url", "http://127.0.0.1:9999/", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	cfg, err := readConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIURL != "http://127.0.0.1:9999" {
		t.Fatalf("apiUrl %q", cfg.APIURL)
	}
	if !strings.Contains(out, `"apiUrl":"http://127.0.0.1:9999"`) {
		t.Fatalf("json output lacks apiUrl: %s", out)
	}
}

func TestInitKeepsTheStoredAPIURLOnRerun(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	writeStored(t, cfgPath, storedConfig{File: config.File{Domain: "example.com", APIKey: "key", APIURL: "http://10.0.0.5:8787"}})
	if code, out := initWithRecovery(t, cfgPath); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	cfg, _ := readConfig(cfgPath)
	if cfg.APIURL != "http://10.0.0.5:8787" {
		t.Fatalf("apiUrl %q", cfg.APIURL)
	}
	if code, out := initWithRecovery(t, cfgPath, "--api-url", "https://api.example.com"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	cfg, _ = readConfig(cfgPath)
	if cfg.APIURL != "https://api.example.com" {
		t.Fatalf("apiUrl %q", cfg.APIURL)
	}
}

func TestInitRejectsAnInvalidAPIURL(t *testing.T) {
	for _, bad := range []string{"ftp://x", "127.0.0.1:8787", "http://"} {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		code, out := initWithRecovery(t, cfgPath, "--api-url", bad)
		if code != 2 || !strings.Contains(out, "--api-url must be") {
			t.Fatalf("%q: exit %d: %s", bad, code, out)
		}
	}
}

// The Stalwart image declares a volume at /etc/stalwart. Without a mount of
// our own, Docker makes an anonymous volume per container.
func TestStackComposeMountsStalwartConfigDir(t *testing.T) {
	svc := regexp.MustCompile(`(?s)\n  stalwart:\n.*?(\n\S|\z)`).Find(stackCompose)
	// The image runs as uid 2000. A tmpfs is root-owned by default, and the
	// entrypoint then cannot write config.json, so Stalwart never starts.
	if !regexp.MustCompile(`(?m)^    tmpfs:\n      - /etc/stalwart:uid=2000,gid=2000$`).Match(svc) {
		t.Fatalf("stalwart service has no /etc/stalwart tmpfs owned by the stalwart user:\n%s", svc)
	}
	if !strings.Contains(string(svc), "- stalwart:/var/lib/stalwart") {
		t.Fatal("stalwart data volume mount changed")
	}
}
