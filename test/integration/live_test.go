// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
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
	must(t, compose, env, "docker", "compose", "-p", "inbai-live", "up", "-d", "--build")
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
	createdOut := must(t, root, cli, filepath.Join(bin, "inbai"), "inbox", "create", "--config", cfg, "--json", "alice")
	var alice struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal([]byte(createdOut), &alice); err != nil || alice.ID == "" {
		t.Fatalf("create %s: %v", createdOut, err)
	}
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

	// Bulk generate 10 addresses against live Stalwart.
	genOut := must(t, root, cli, filepath.Join(bin, "inbai"), "inbox", "generate", "--config", cfg, "--prefix", "gen", "--count", "10", "--json")
	var generated struct {
		Inboxes []struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		} `json:"inboxes"`
	}
	if err := json.Unmarshal([]byte(genOut), &generated); err != nil || len(generated.Inboxes) != 10 {
		t.Fatalf("generate 10 %s: %v", genOut, err)
	}

	// OAuth credentials, grants, isolation, and MCP reply against live Stalwart.
	const baseURL = "http://127.0.0.1:8787"
	var adaCred, beaCred struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	adaOut := must(t, root, cli, filepath.Join(bin, "inbai"), "agent", "create", "--config", cfg, "--json", "ada")
	if err := json.Unmarshal([]byte(adaOut), &adaCred); err != nil || adaCred.ID == "" {
		t.Fatalf("agent ada %s: %v", adaOut, err)
	}
	beaOut := must(t, root, cli, filepath.Join(bin, "inbai"), "agent", "create", "--config", cfg, "--json", "bea")
	if err := json.Unmarshal([]byte(beaOut), &beaCred); err != nil || beaCred.ID == "" {
		t.Fatalf("agent bea %s: %v", beaOut, err)
	}
	for _, perm := range []string{"read", "send", "reply"} {
		must(t, root, cli, filepath.Join(bin, "inbai"), "agent", "grant", "--config", cfg, "ada", alice.ID, perm)
	}
	adaTok := exchangeToken(t, baseURL, adaCred.ID, adaCred.Secret)
	beaTok := exchangeToken(t, baseURL, beaCred.ID, beaCred.Secret)

	// bea has no grant on alice and is blocked on REST and MCP.
	doJSON(t, baseURL, beaTok, http.MethodGet, "/v1/inboxes/"+alice.ID+"/messages", "", http.StatusForbidden, nil)
	beaMCP := doRaw(t, baseURL, beaTok, http.MethodPost, "/mcp",
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":%q}}}`, alice.ID),
		http.StatusOK)
	if !strings.Contains(beaMCP, `"isError":true`) {
		t.Fatalf("bea MCP read alice: %s", beaMCP)
	}

	// ada lists and replies over MCP.
	adaMCPList := doRaw(t, baseURL, adaTok, http.MethodPost, "/mcp",
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":%q}}}`, alice.ID),
		http.StatusOK)
	if !strings.Contains(adaMCPList, "hello") || !strings.Contains(adaMCPList, "untrusted external content") {
		t.Fatalf("ada MCP list: %s", adaMCPList)
	}
	adaMCPReply := doRaw(t, baseURL, adaTok, http.MethodPost, "/mcp",
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"reply_message","arguments":{"inbox":%q,"messageId":%q,"from":"alice@inbai.test","to":"alice@inbai.test","text":"reply from mcp"}}}`, alice.ID, id),
		http.StatusOK)
	if strings.Contains(adaMCPReply, `"isError":true`) {
		t.Fatalf("ada MCP reply: %s", adaMCPReply)
	}

	replyDeadline := time.Now().Add(45 * time.Second)
	sawReply := false
	for time.Now().Before(replyDeadline) {
		body := doRaw(t, baseURL, adaTok, http.MethodPost, "/mcp",
			fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":%q}}}`, alice.ID),
			http.StatusOK)
		if strings.Contains(body, "Re: hello") {
			sawReply = true
			break
		}
		time.Sleep(time.Second)
	}
	if !sawReply {
		t.Fatal("MCP reply did not arrive")
	}

	// Event feed survives inbaid stopping while a message is delivered to Stalwart.
	var before struct {
		Events []struct {
			ID int64 `json:"id"`
		} `json:"events"`
	}
	doJSON(t, baseURL, adaTok, http.MethodGet, "/v1/events?inbox="+url.QueryEscape(alice.ID)+"&after=0&timeout=1", "", http.StatusOK, &before)
	var lastEvent int64
	for _, ev := range before.Events {
		if ev.ID > lastEvent {
			lastEvent = ev.ID
		}
	}

	must(t, compose, env, "docker", "compose", "-p", "inbai-live", "stop", "inbaid")
	deliverDirect(t, cfg, alice.ID, "alice@inbai.test", "while stopped", "delivered while inbaid was down")
	must(t, compose, env, "docker", "compose", "-p", "inbai-live", "start", "inbaid")
	waitURL(t, baseURL+"/healthz", false)

	eventDeadline := time.Now().Add(60 * time.Second)
	sawEvent := false
	for time.Now().Before(eventDeadline) {
		var after struct {
			Events []struct {
				ID   int64  `json:"id"`
				Kind string `json:"kind"`
			} `json:"events"`
		}
		doJSON(t, baseURL, adaTok, http.MethodGet,
			fmt.Sprintf("/v1/events?inbox=%s&after=%d&timeout=2", url.QueryEscape(alice.ID), lastEvent),
			"", http.StatusOK, &after)
		for _, ev := range after.Events {
			if ev.Kind == "created" {
				sawEvent = true
				break
			}
		}
		if sawEvent {
			break
		}
		time.Sleep(time.Second)
	}
	if !sawEvent {
		t.Fatal("event delivered while inbaid was stopped did not appear on the feed")
	}

	// Storage quota get/set against live Stalwart 1.1.1.
	qOut := must(t, root, cli, filepath.Join(bin, "inbai"), "inbox", "quota", "get", "--config", cfg, "--json", alice.ID)
	var quota struct {
		LimitBytes int64 `json:"limitBytes"`
		UsedBytes  int64 `json:"usedBytes"`
	}
	if err := json.Unmarshal([]byte(qOut), &quota); err != nil || quota.LimitBytes != 104857600 {
		t.Fatalf("initial storage quota %s: %v", qOut, err)
	}
	qSetOut := must(t, root, cli, filepath.Join(bin, "inbai"), "inbox", "quota", "set", "--config", cfg, "--json", alice.ID, "209715200")
	if err := json.Unmarshal([]byte(qSetOut), &quota); err != nil || quota.LimitBytes != 209715200 {
		t.Fatalf("updated storage quota %s: %v", qSetOut, err)
	}

	// Operator login saves credentials in config.json so later commands need no env var.
	loginCfg := filepath.Join(t.TempDir(), "operator.json")
	must(t, root, cli, filepath.Join(bin, "inbai"), "login", "--url", baseURL, "--config", loginCfg, "--json")
	listOut := must(t, root, nil, filepath.Join(bin, "inbai"), "inbox", "list", "--config", loginCfg, "--json")
	if !strings.Contains(listOut, "alice@inbai.test") {
		t.Fatalf("inbox list after login: %s", listOut)
	}
}

func deliverDirect(t *testing.T, cfgPath, accountID, address, subject, body string) {
	t.Helper()
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.File
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	client := &stalwart.Client{
		BaseURL: "https://127.0.0.1",
		APIKey:  cfg.APIKey,
		HTTP: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // local Stalwart certificate is self-signed
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := stalwart.Deliver(ctx, client, stalwart.Delivery{
		AccountID: accountID,
		From:      address,
		To:        address,
		Subject:   subject,
		Body:      body,
	}); err != nil {
		t.Fatalf("direct deliver: %v", err)
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
