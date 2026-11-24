// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
)

const recoveryPasswordEnv = "INBAI_RECOVERY_PASSWORD"

// run dispatches args through the command table in commands.go.
func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	return program.Main(args, stdout, stderr, getenv)
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// submissionPortNotice is what init prints when --submission-port is given.
const submissionPortNotice = "--submission-port is ignored: inbAI does not publish SMTP submission; agents send through the API"

func initCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		writef(stderr, "usage: inbai init --domain name [--public] [--no-docker] [--url url] [--api-url url] [--hostname name] [--stalwart-url url] [--stack-dir dir] [--config path] [--json]\n")
		fs.PrintDefaults()
	}
	url := fs.String("url", "http://127.0.0.1:8080", "Stalwart recovery API, reachable on the host (skips local Docker setup)")
	domain := fs.String("domain", "", "DNS domain to create")
	hostname := fs.String("hostname", "", "mail hostname (default mail.<domain>)")
	apiURLFlag := fs.String("api-url", "", "inbaid API URL to store for later commands (default: keep the stored one, else http://127.0.0.1:8787)")
	stalwartURL := fs.String("stalwart-url", "", "Stalwart URL to store for later commands")
	configPath := fs.String("config", "", "config file to write (default inbai/config.json in the user config directory)")
	stackDir := fs.String("stack-dir", "", "directory for local Docker Compose files (default <config-dir>/stack)")
	public := fs.Bool("public", false, "bind SMTP (25) and HTTPS (443) on all interfaces in Docker mode")
	pgPort := fs.Int("pg-port", 5433, "local PostgreSQL port in Docker mode")
	httpsPort := fs.Int("https-port", 0, "Stalwart HTTPS port in Docker mode (default 8443, or 443 with --public)")
	smtpPort := fs.Int("smtp-port", 0, "Stalwart SMTP port in Docker mode (default 2525, or 25 with --public)")
	// --submission-port is kept so existing scripts still parse. It does nothing.
	fs.Int("submission-port", 0, "deprecated and ignored: inbAI does not publish SMTP submission")
	recoveryPort := fs.Int("recovery-port", 8080, "Stalwart recovery port in Docker mode")
	noDocker := fs.Bool("no-docker", false, "connect to an already-running Stalwart instead of starting Docker Compose")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *domain == "" {
		writef(stderr, "init needs --domain\n")
		return 2
	}
	apiURL := strings.TrimSpace(*apiURLFlag)
	if apiURL != "" {
		var ok bool
		if apiURL, ok = checkAPIURL(apiURL); !ok {
			writef(stderr, "init: --api-url must be an http or https URL\n")
			return 2
		}
	}
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	if visited["submission-port"] {
		writef(stderr, "%s\n", submissionPortNotice)
	}

	password := ""
	if getenv != nil {
		password = getenv(recoveryPasswordEnv)
	}
	useExternal := *noDocker || visited["url"] || password != ""
	if useExternal && password == "" {
		writef(stderr, "init needs %s\n", recoveryPasswordEnv)
		return 2
	}

	path := *configPath
	if path == "" {
		var err error
		path, err = configFilePath("")
		if err != nil {
			writef(stderr, "config directory: %s\n", err)
			return 1
		}
	}
	existing, err := readStoredConfig(path)
	if err != nil {
		writef(stderr, "read config: %s\n", err)
		return 1
	}
	if existing.OperatorToken == "" && getenv != nil {
		if supplied := strings.TrimSpace(getenv("INBAI_OPERATOR_TOKEN")); supplied != "" {
			if err := config.CheckOperatorToken(supplied); err != nil {
				writef(stderr, "init: %s\n", err)
				return 2
			}
		}
	}
	if existing.Domain != "" && existing.Domain != *domain {
		writef(stderr, "config already records domain %s\n", existing.Domain)
		return 1
	}
	// Create the config directory before the stack directory inside it, so a
	// new one gets 0700. MkdirAll leaves an existing directory's mode alone.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		writef(stderr, "config directory: %s\n", err)
		return 1
	}

	var (
		result           stalwart.Result
		finalStalwart    string
		finalDBURL       = existing.DatabaseURL
		finalStackDir    = existing.StackDir
		operatorPassword = existing.OperatorPassword
		usedDockerMode   = !useExternal
	)
	if useExternal {
		finalStalwart = *stalwartURL
		if finalStalwart == "" {
			finalStalwart = "https://127.0.0.1"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result, err = stalwart.Bootstrap(ctx, &stalwart.Client{
			BaseURL:  *url,
			User:     "admin",
			Password: password,
		}, stalwart.Input{
			Domain:           *domain,
			Hostname:         *hostname,
			OperatorPassword: password,
			CreateAPIKey:     existing.APIKey == "",
		})
		if err != nil {
			writef(stderr, "init: %s\n", err)
			return 1
		}
	} else {
		dir := *stackDir
		if dir == "" {
			if existing.StackDir != "" {
				dir = existing.StackDir
			} else {
				dir = filepath.Join(filepath.Dir(path), "stack")
			}
		}
		project := ""
		if getenv != nil {
			project = getenv("COMPOSE_PROJECT_NAME")
		}
		var progressOut io.Writer
		if !*asJSON {
			progressOut = stderr
		}
		// A config from an older inbai has no operator password. Make one and
		// set it on the operator account, which may already exist.
		resetOperator := operatorPassword == ""
		if resetOperator {
			operatorPassword, err = randomHex(24)
			if err != nil {
				writef(stderr, "generate operator password: %s\n", err)
				return 1
			}
		}
		dockerRes, err := initDockerStack(context.Background(), dockerInitOptions{
			Domain:       *domain,
			Hostname:     *hostname,
			StackDir:     dir,
			Project:      project,
			Public:       *public,
			PGPort:       *pgPort,
			HTTPSPort:    *httpsPort,
			SMTPPort:     *smtpPort,
			RecoveryPort: *recoveryPort,
			StalwartURL:  *stalwartURL,
			Existing:     existing.File,
			Progress:     progressOut,

			OperatorPassword:      operatorPassword,
			ResetOperatorPassword: resetOperator,
		})
		if err != nil {
			writef(stderr, "init: %s\n", err)
			return 1
		}
		result = dockerRes.Bootstrap
		finalStalwart = dockerRes.StalwartURL
		finalDBURL = dockerRes.DatabaseURL
		finalStackDir = dockerRes.StackDir
	}

	operatorToken := existing.OperatorToken
	if operatorToken == "" && getenv != nil {
		operatorToken = strings.TrimSpace(getenv("INBAI_OPERATOR_TOKEN"))
	}
	if operatorToken == "" {
		operatorToken, err = generateOperatorToken()
		if err != nil {
			writef(stderr, "operator token: %s\n", err)
			return 1
		}
	}

	cfg := config.File{
		Domain:            *domain,
		Hostname:          result.Hostname,
		StalwartURL:       finalStalwart,
		ServiceAccountID:  result.ServiceAccountID,
		OperatorAccountID: result.OperatorAccountID,
		InboxRoleID:       result.InboxRoleID,
		ServiceRoleID:     result.ServiceRoleID,
		DomainID:          result.DomainID,
		Inboxes:           existing.Inboxes,
		APIKey:            result.APIKey,
		APIURL:            config.DefaultAPIURL,
		ClientID:          existing.ClientID,
		ClientSecret:      existing.ClientSecret,
		OperatorToken:     operatorToken,
		DatabaseURL:       finalDBURL,
		StackDir:          finalStackDir,
		RetentionDays:     config.DefaultRetentionDays,
	}
	// A config with a domain came from an earlier init. Keep its retention,
	// including 0, which inbai retention --days 0 writes to keep mail.
	if existing.Domain != "" {
		cfg.RetentionDays = existing.RetentionDays
	}
	if cfg.APIKey == "" {
		cfg.APIKey = existing.APIKey
	}
	if existing.APIURL != "" {
		cfg.APIURL = existing.APIURL
	}
	if apiURL != "" {
		cfg.APIURL = apiURL
	}
	if err := writeStoredConfig(path, storedConfig{File: cfg, OperatorPassword: operatorPassword}); err != nil {
		writef(stderr, "write config: %s\n", err)
		if result.APIKey != "" {
			writef(stderr, "API key, shown once: %s\n", result.APIKey)
		}
		return 1
	}
	if *asJSON {
		payload := map[string]any{
			"domain":         cfg.Domain,
			"hostname":       cfg.Hostname,
			"operator":       "operator@" + cfg.Domain,
			"serviceAccount": "service@" + cfg.Domain,
			"retentionDays":  cfg.RetentionDays,
			"apiUrl":         cfg.APIURL,
			"config":         path,
		}
		if usedDockerMode {
			payload["stackDir"] = cfg.StackDir
			payload["stalwartUrl"] = cfg.StalwartURL
			payload["next"] = "Run inbaid serve to start the API server."
		} else {
			payload["next"] = "Set STALWART_RECOVERY_MODE=0 and start the stack again before sending mail."
		}
		emit(stdout, true, "", payload)
		return 0
	}
	writef(stdout, "Domain %s is ready.\n", cfg.Domain)
	writef(stdout, "Operator login is operator@%s.\n", cfg.Domain)
	writef(stdout, "Service account is service@%s.\n", cfg.Domain)
	if cfg.RetentionDays == 0 {
		writef(stdout, "Messages are kept until they are deleted.\n")
	} else {
		writef(stdout, "Message retention is %d days.\n", cfg.RetentionDays)
	}
	writef(stdout, "API URL is %s.\n", cfg.APIURL)
	writef(stdout, "Wrote %s\n", path)
	if usedDockerMode {
		writef(stdout, "The operator password is stored in %s.\n", path)
		writef(stdout, "Stack files are in %s\n", cfg.StackDir)
		writef(stdout, "Start the server with: inbaid serve\n")
		writef(stdout, "Stop the stack with: docker compose -f %q down\n", filepath.Join(cfg.StackDir, "compose.yaml"))
		return 0
	}
	writef(stdout, "Set STALWART_RECOVERY_MODE=0 and start the stack again before sending mail.\n")
	return 0
}

// checkAPIURL reports whether raw is an http or https URL with a host, and
// returns it without a trailing slash.
func checkAPIURL(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	return strings.TrimRight(raw, "/"), true
}

func configFilePath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "inbai", "config.json"), nil
}

// storedConfig is config.json as inbai writes it. The operator password lives
// here, not in config.File, because only the CLI reads it. It is a secret,
// like the API key, so the file mode stays 0600.
type storedConfig struct {
	config.File
	OperatorPassword string `json:"operatorPassword,omitempty"`
}

func readConfig(path string) (config.File, error) {
	stored, err := readStoredConfig(path)
	return stored.File, err
}

func readStoredConfig(path string) (storedConfig, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return storedConfig{}, nil
	}
	if err != nil {
		return storedConfig{}, err
	}
	var cfg storedConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return storedConfig{}, err
	}
	return cfg, nil
}

// writeConfig replaces the config file and keeps the operator password
// already stored there.
func writeConfig(path string, cfg config.File) error {
	stored, _ := readStoredConfig(path)
	return writeStoredConfig(path, storedConfig{File: cfg, OperatorPassword: stored.OperatorPassword})
}

// syncConfigFile flushes the new config file before the rename. Tests replace it.
var syncConfigFile = (*os.File).Sync

// writeStoredConfig writes a temporary file next to path and renames it over
// path, so a crash leaves either the old file or the new one. It does not lock:
// two commands that write at once each succeed, and the last rename wins.
func writeStoredConfig(path string, cfg storedConfig) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	// CreateTemp opens the file with mode 0600.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(raw); err != nil {
		return err
	}
	if err := syncConfigFile(tmp); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// os.Rename replaces an existing file on Windows as well.
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
