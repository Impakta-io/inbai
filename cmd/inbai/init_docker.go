// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	_ "embed"
	"encoding/hex"
	"errors"
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

// composeRunner runs docker compose. With out set, it streams the command's
// output there and returns no bytes. Otherwise it returns the combined output.
type composeRunner func(ctx context.Context, dir string, env []string, out io.Writer, args ...string) ([]byte, error)

type urlWaiter func(ctx context.Context, rawURL string, insecure bool) error

var (
	runCompose   composeRunner = execDockerCompose
	waitForURL   urlWaiter     = pollURL
	pollInterval               = 500 * time.Millisecond
	// pullTimeout bounds the image pull. readyTimeout bounds everything after it.
	pullTimeout  = 30 * time.Minute
	readyTimeout = 5 * time.Minute
)

type dockerInitOptions struct {
	Domain       string
	Hostname     string
	StackDir     string
	Project      string
	Public       bool
	PGPort       int
	HTTPSPort    int
	SMTPPort     int
	RecoveryPort int
	StalwartURL  string
	Existing     config.File
	Progress     io.Writer

	// OperatorPassword is set on operator@<domain>. ResetOperatorPassword
	// also sets it when that account already exists.
	OperatorPassword      string
	ResetOperatorPassword bool
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
	} else {
		if o.HTTPSPort == 0 {
			o.HTTPSPort = 8443
		}
		if o.SMTPPort == 0 {
			o.SMTPPort = 2525
		}
	}
	for name, port := range map[string]int{
		"pg-port":       o.PGPort,
		"https-port":    o.HTTPSPort,
		"smtp-port":     o.SMTPPort,
		"recovery-port": o.RecoveryPort,
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
	if opts.OperatorPassword == "" {
		return dockerInitResult{}, fmt.Errorf("operator password is empty")
	}
	if out, err := runCompose(ctx, ".", nil, nil, "version"); err != nil {
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

	// Pull first, with its own timeout, so a slow download is visible and
	// does not use up the time Stalwart has to start.
	progress(opts.Progress, "Pulling the PostgreSQL and Stalwart images...\n")
	pullCtx, cancelPull := context.WithTimeout(ctx, pullTimeout)
	out, err := runCompose(pullCtx, opts.StackDir, nil, opts.Progress, "-f", "compose.yaml", "pull", "--policy", "missing")
	cancelPull()
	if err != nil {
		return dockerInitResult{}, fmt.Errorf("docker compose pull: %w\n%s", err, trimTail(string(out), 2000))
	}

	recoveryPassword, err := randomHex(24)
	if err != nil {
		return dockerInitResult{}, fmt.Errorf("generate recovery password: %w", err)
	}
	stack := dockerStack{
		dir:              opts.StackDir,
		project:          project,
		recoveryURL:      fmt.Sprintf("http://127.0.0.1:%d", opts.RecoveryPort),
		httpsURL:         strings.TrimRight(opts.StalwartURL, "/"),
		recoveryPassword: recoveryPassword,
		progress:         opts.Progress,
	}

	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()

	var boot stalwart.Result
	progress(opts.Progress, "Starting PostgreSQL and Stalwart with Docker Compose...\n")
	err = stack.inRecovery(ctx, []string{"up", "-d"}, func() error {
		progress(opts.Progress, "Bootstrapping domain %s...\n", opts.Domain)
		var err error
		boot, err = stalwart.Bootstrap(ctx, stack.admin(), stalwart.Input{
			Domain:                opts.Domain,
			Hostname:              opts.Hostname,
			OperatorPassword:      opts.OperatorPassword,
			ResetOperatorPassword: opts.ResetOperatorPassword,
			CreateAPIKey:          opts.Existing.APIKey == "",
		})
		return err
	})
	if err != nil {
		return dockerInitResult{}, err
	}

	// The stored key belongs to the Stalwart database that issued it. After
	// docker compose down -v that database is gone. Issue a new key only when
	// Stalwart answers and refuses the old one.
	if opts.Existing.APIKey != "" {
		err := stalwart.CheckAPIKey(ctx, &stalwart.Client{BaseURL: stack.httpsURL, APIKey: opts.Existing.APIKey, HTTP: insecureHTTP()})
		switch {
		case err == nil:
		case errors.Is(err, stalwart.ErrAPIKeyRejected):
			progress(opts.Progress, "Stalwart rejected the stored API key. Issuing a new one...\n")
			err = stack.inRecovery(ctx, []string{"up", "-d", "--force-recreate", "stalwart"}, func() error {
				key, err := stalwart.CreateAPIKey(ctx, stack.admin(), boot.ServiceAccountID)
				boot.APIKey = key
				return err
			})
			if err != nil {
				return dockerInitResult{}, fmt.Errorf("replace the API key Stalwart rejected: %w", err)
			}
		default:
			progress(opts.Progress, "Could not check the stored API key, so it is kept: %v\n", err)
		}
	}

	return dockerInitResult{
		Bootstrap:   boot,
		DatabaseURL: fmt.Sprintf("postgres://stalwart:%s@127.0.0.1:%d/inbai?sslmode=disable", url.QueryEscape(pgPassword), opts.PGPort),
		StalwartURL: opts.StalwartURL,
		StackDir:    opts.StackDir,
	}, nil
}

// dockerStack is the local Compose stack while init runs.
type dockerStack struct {
	dir              string
	project          string
	recoveryURL      string
	httpsURL         string
	recoveryPassword string
	progress         io.Writer
}

func (s dockerStack) admin() *stalwart.Client {
	return &stalwart.Client{BaseURL: s.recoveryURL, User: "admin", Password: s.recoveryPassword}
}

// inRecovery runs docker compose with upArgs in recovery mode, waits for the
// recovery API, calls fn, and restarts Stalwart in normal mode. It leaves
// recovery mode when a step fails, too.
func (s dockerStack) inRecovery(ctx context.Context, upArgs []string, fn func() error) error {
	recoveryEnv := []string{
		"STALWART_RECOVERY_MODE=1",
		"STALWART_RECOVERY_ADMIN=admin:" + s.recoveryPassword,
	}
	if out, err := runCompose(ctx, s.dir, recoveryEnv, nil, append([]string{"-f", "compose.yaml"}, upArgs...)...); err != nil {
		return fmt.Errorf("docker compose up: %w\n%s", err, trimTail(string(out), 2000))
	}
	cleanup := func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.leaveRecovery(c)
	}
	progress(s.progress, "Waiting for Stalwart recovery API...\n")
	if err := s.wait(ctx, "Stalwart recovery API", s.recoveryURL+"/jmap/", false); err != nil {
		cleanup()
		return err
	}
	if err := fn(); err != nil {
		cleanup()
		return err
	}
	progress(s.progress, "Switching Stalwart to normal mode...\n")
	if err := s.leaveRecovery(ctx); err != nil {
		return err
	}
	return s.wait(ctx, "Stalwart HTTPS API", s.httpsURL+"/jmap/", true)
}

func (s dockerStack) leaveRecovery(ctx context.Context) error {
	normalEnv := []string{
		"STALWART_RECOVERY_MODE=0",
		"STALWART_RECOVERY_ADMIN=",
	}
	out, err := runCompose(ctx, s.dir, normalEnv, nil, "-f", "compose.yaml", "up", "-d", "--force-recreate", "stalwart")
	if err != nil {
		return fmt.Errorf("restart stalwart out of recovery mode: %w\n%s", err, trimTail(string(out), 2000))
	}
	return nil
}

// wait waits for rawURL. When the time runs out, the error carries the end of
// the stack's logs. They are read here, before a restart replaces the container.
func (s dockerStack) wait(ctx context.Context, what, rawURL string, insecure bool) error {
	err := waitForURL(ctx, rawURL, insecure)
	if err == nil {
		return nil
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("wait for %s: %w", what, err)
	}
	logCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logs, logErr := runCompose(logCtx, s.dir, nil, nil, "-f", "compose.yaml", "logs", "--no-color", "--tail", "50")
	var b strings.Builder
	fmt.Fprintf(&b, "%s did not answer within %s: %v\n", what, readyTimeout, err)
	if logErr != nil {
		fmt.Fprintf(&b, "docker compose logs failed: %v\n", logErr)
	}
	if tail := lastLines(string(logs), 50); tail != "" {
		fmt.Fprintf(&b, "Last lines of docker compose logs:\n%s\n", tail)
	}
	fmt.Fprintf(&b, "If an earlier install used Compose project %s, its volumes may hold a different PostgreSQL password.\n", s.project)
	b.WriteString("Removing them deletes that install's mail and accounts. To remove them, run:\n")
	fmt.Fprintf(&b, "  docker compose -p %s -f %q down -v\n", s.project, filepath.Join(s.dir, "compose.yaml"))
	b.WriteString("Then run inbai init again.")
	return errors.New(b.String())
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

func execDockerCompose(ctx context.Context, dir string, env []string, out io.Writer, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out != nil {
		cmd.Stdout = out
		cmd.Stderr = out
		return nil, cmd.Run()
	}
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

// lastLines returns the last n lines of s.
func lastLines(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func trimTail(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
