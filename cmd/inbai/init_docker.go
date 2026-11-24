// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
)

//go:embed stack/compose.yaml
var stackCompose []byte

//go:embed stack/entrypoint.sh
var stackEntrypoint []byte

//go:embed stack/01-inbai.sql
var stackInitSQL []byte

type composeRunner func(ctx context.Context, dir string, env []string, args ...string) ([]byte, error)

type urlWaiter func(ctx context.Context, rawURL string, insecure bool) error

var (
	runCompose   composeRunner = execDockerCompose
	waitForURL   urlWaiter     = pollURL
	pollInterval               = 500 * time.Millisecond
)

type dockerInitOptions struct {
	Domain         string
	Hostname       string
	StackDir       string
	Project        string
	Public         bool
	PGPort         int
	HTTPSPort      int
	SMTPPort       int
	SubmissionPort int
	RecoveryPort   int
	StalwartURL    string
	Existing       config.File
	Progress       io.Writer
}

type dockerInitResult struct {
	Bootstrap   stalwart.Result
	DatabaseURL string
	StalwartURL string
	StackDir    string
}

func (o *dockerInitOptions) applyDefaults() error {
	if o.PGPort == 0 {
		o.PGPort = 5433
	}
	if o.RecoveryPort == 0 {
		o.RecoveryPort = 8080
	}
	if o.Public {
		if o.HTTPSPort == 0 {
			o.HTTPSPort = 443
		}
		if o.SMTPPort == 0 {
			o.SMTPPort = 25
		}
		if o.SubmissionPort == 0 {
			o.SubmissionPort = 587
		}
	} else {
		if o.HTTPSPort == 0 {
			o.HTTPSPort = 8443
		}
		if o.SMTPPort == 0 {
			o.SMTPPort = 2525
		}
		if o.SubmissionPort == 0 {
			o.SubmissionPort = 5870
		}
	}
	for name, port := range map[string]int{
		"pg-port":         o.PGPort,
		"https-port":      o.HTTPSPort,
		"smtp-port":       o.SMTPPort,
		"submission-port": o.SubmissionPort,
		"recovery-port":   o.RecoveryPort,
	} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("--%s must be between 1 and 65535", name)
		}
	}
	if o.StalwartURL == "" {
		o.StalwartURL = httpsURLForPort(o.HTTPSPort)
	}
	return nil
}

func initDockerStack(ctx context.Context, opts dockerInitOptions) (dockerInitResult, error) {
	if err := opts.applyDefaults(); err != nil {
		return dockerInitResult{}, err
	}
	if out, err := runCompose(ctx, ".", nil, "version"); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return dockerInitResult{}, fmt.Errorf("docker compose is required to start the local stack (%v: %s); install Docker or pass --url with %s", err, msg, recoveryPasswordEnv)
		}
		return dockerInitResult{}, fmt.Errorf("docker compose is required to start the local stack (%v); install Docker or pass --url with %s", err, recoveryPasswordEnv)
	}

	pgPassword, err := resolvePGPassword(opts.StackDir, opts.Existing)
	if err != nil {
		return dockerInitResult{}, err
	}
	project := strings.TrimSpace(opts.Project)
	if project == "" {
		project = readEnvValue(filepath.Join(opts.StackDir, ".env"), "COMPOSE_PROJECT_NAME")
	}
	if project == "" {
		project = "inbai"
	}

	if err := writeStackFiles(opts.StackDir, stackEnv(project, pgPassword, opts)); err != nil {
		return dockerInitResult{}, fmt.Errorf("write stack files: %w", err)
	}

	recoveryPassword, err := randomHex(24)
	if err != nil {
		return dockerInitResult{}, fmt.Errorf("generate recovery password: %w", err)
	}
	recoveryEnv := []string{
		"STALWART_RECOVERY_MODE=1",
		"STALWART_RECOVERY_ADMIN=admin:" + recoveryPassword,
	}
	normalEnv := []string{
		"STALWART_RECOVERY_MODE=0",
		"STALWART_RECOVERY_ADMIN=",
	}

	progress(opts.Progress, "Starting PostgreSQL and Stalwart with Docker Compose...\n")
	if out, err := runCompose(ctx, opts.StackDir, recoveryEnv, "-f", "compose.yaml", "up", "-d"); err != nil {
		return dockerInitResult{}, fmt.Errorf("docker compose up: %w\n%s", err, trimTail(string(out), 2000))
	}

	leaveRecovery := func(c context.Context) error {
		out, err := runCompose(c, opts.StackDir, normalEnv, "-f", "compose.yaml", "up", "-d", "--force-recreate", "stalwart")
		if err != nil {
			return fmt.Errorf("restart stalwart out of recovery mode: %w\n%s", err, trimTail(string(out), 2000))
		}
		return nil
	}

	recoveryURL := fmt.Sprintf("http://127.0.0.1:%d", opts.RecoveryPort)
	progress(opts.Progress, "Waiting for Stalwart recovery API...\n")
	if err := waitForURL(ctx, recoveryURL+"/jmap/", false); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = leaveRecovery(cleanupCtx)
		cancel()
		return dockerInitResult{}, fmt.Errorf("wait for Stalwart recovery API: %w", err)
	}

	progress(opts.Progress, "Bootstrapping domain %s...\n", opts.Domain)
	boot, err := stalwart.Bootstrap(ctx, &stalwart.Client{
		BaseURL:  recoveryURL,
		User:     "admin",
		Password: recoveryPassword,
	}, stalwart.Input{
		Domain:           opts.Domain,
		Hostname:         opts.Hostname,
		OperatorPassword: recoveryPassword,
		CreateAPIKey:     opts.Existing.APIKey == "",
	})
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = leaveRecovery(cleanupCtx)
		cancel()
		return dockerInitResult{}, err
	}

	progress(opts.Progress, "Switching Stalwart to normal mode...\n")
	if err := leaveRecovery(ctx); err != nil {
		return dockerInitResult{}, err
	}
	if err := waitForURL(ctx, strings.TrimRight(opts.StalwartURL, "/")+"/jmap/", true); err != nil {
		return dockerInitResult{}, fmt.Errorf("wait for Stalwart HTTPS API: %w", err)
	}

	return dockerInitResult{
		Bootstrap:   boot,
		DatabaseURL: fmt.Sprintf("postgres://stalwart:%s@127.0.0.1:%d/inbai?sslmode=disable", url.QueryEscape(pgPassword), opts.PGPort),
		StalwartURL: opts.StalwartURL,
		StackDir:    opts.StackDir,
	}, nil
}

func stackEnv(project, pgPassword string, opts dockerInitOptions) string {
	bind := func(port int) string {
		if opts.Public {
			return fmt.Sprintf("%d", port)
		}
		return fmt.Sprintf("127.0.0.1:%d", port)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "COMPOSE_PROJECT_NAME=%s\n", project)
	fmt.Fprintf(&b, "PG_PASSWORD=%s\n", pgPassword)
	fmt.Fprintf(&b, "PG_PORT=%d\n", opts.PGPort)
	fmt.Fprintf(&b, "SMTP_BIND=%s\n", bind(opts.SMTPPort))
	fmt.Fprintf(&b, "SUBMISSION_BIND=%s\n", bind(opts.SubmissionPort))
	fmt.Fprintf(&b, "HTTPS_BIND=%s\n", bind(opts.HTTPSPort))
	fmt.Fprintf(&b, "RECOVERY_PORT=%d\n", opts.RecoveryPort)
	b.WriteString("STALWART_RECOVERY_MODE=0\n")
	return b.String()
}

func writeStackFiles(dir, envContent string) error {
	initdbDir := filepath.Join(dir, "initdb")
	if err := os.MkdirAll(initdbDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), stackCompose, 0o644); err != nil {
		return err
	}
	entryPath := filepath.Join(dir, "entrypoint.sh")
	if err := os.WriteFile(entryPath, stackEntrypoint, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(entryPath, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(initdbDir, "01-inbai.sql"), stackInitSQL, 0o644); err != nil {
		return err
	}
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte(envContent), 0o600); err != nil {
		return err
	}
	return os.Chmod(envPath, 0o600)
}

func resolvePGPassword(stackDir string, existing config.File) (string, error) {
	if pw := readEnvValue(filepath.Join(stackDir, ".env"), "PG_PASSWORD"); pw != "" {
		return pw, nil
	}
	if pw := passwordFromDatabaseURL(existing.DatabaseURL); pw != "" {
		return pw, nil
	}
	return randomHex(24)
}

func readEnvValue(path, key string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func passwordFromDatabaseURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return ""
	}
	pw, _ := parsed.User.Password()
	return pw
}

func httpsURLForPort(port int) string {
	if port == 443 {
		return "https://127.0.0.1"
	}
	return fmt.Sprintf("https://127.0.0.1:%d", port)
}

func generateOperatorToken() (string, error) {
	raw, err := randomHex(32)
	if err != nil {
		return "", err
	}
	return "inb_" + raw, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func execDockerCompose(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	return cmd.CombinedOutput()
}

func pollURL(ctx context.Context, rawURL string, insecure bool) error {
	client := &http.Client{Timeout: 2 * time.Second}
	if insecure {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // local Stalwart uses a self-signed certificate
		}
	}
	var last error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return err
		}
		res, err := client.Do(req)
		if err == nil {
			_ = res.Body.Close()
			return nil
		}
		last = err
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if last != nil {
				return fmt.Errorf("%w (last error: %v)", ctx.Err(), last)
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func progress(w io.Writer, format string, args ...any) {
	if w != nil {
		_, _ = fmt.Fprintf(w, format, args...)
	}
}

func trimTail(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
