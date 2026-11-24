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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
	"github.com/Impakta-io/inbai/internal/version"
)

const recoveryPasswordEnv = "INBAI_RECOVERY_PASSWORD"

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 || args[0] == "version" {
		writef(stdout, "%s\n", version.String(version.CLI))
		return 0
	}
	switch args[0] {
	case "init":
		return initCmd(args[1:], stdout, stderr, getenv)
	case "login":
		return loginCmd(args[1:], stdout, stderr, getenv)
	case "domain":
		return domainCmd(args[1:], stdout, stderr, getenv)
	case "doctor":
		return doctorCmd(args[1:], stdout, stderr)
	case "inbox":
		return inboxCmd(args[1:], stdout, stderr, getenv)
	case "message":
		return messageCmd(args[1:], stdout, stderr, getenv)
	case "agent":
		return agentCmd(args[1:], stdout, stderr, getenv)
	case "audit":
		return auditCmd(args[1:], stdout, stderr, getenv)
	case "retention":
		return retentionCmd(args[1:], stdout, stderr, getenv)
	case "mcp":
		return mcpCmd(args[1:], os.Stdin, stdout, stderr, getenv)
	case "backup":
		return backupCmd(args[1:], stdout, stderr, getenv)
	case "restore":
		return restoreCmd(args[1:], stdout, stderr, getenv)
	case "-h", "--help", "help":
		writef(stdout, "usage: inbai [version|init|login|domain|doctor|inbox|message|agent|audit|retention|mcp|backup|restore]\n")
		return 0
	default:
		writef(stderr, "unknown command %q\n", args[0])
		return 2
	}
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func initCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		writef(stderr, "usage: inbai init --domain name [--public] [--no-docker] [--url url] [--hostname name] [--stalwart-url url] [--stack-dir dir] [--config path] [--json]\n")
		fs.PrintDefaults()
	}
	url := fs.String("url", "http://127.0.0.1:8080", "Stalwart recovery API, reachable on the host (skips local Docker setup)")
	domain := fs.String("domain", "", "DNS domain to create")
	hostname := fs.String("hostname", "", "mail hostname (default mail.<domain>)")
	stalwartURL := fs.String("stalwart-url", "", "Stalwart URL to store for later commands")
	configPath := fs.String("config", "", "config file to write (default is the user config directory)")
	stackDir := fs.String("stack-dir", "", "directory for local Docker Compose files (default <config-dir>/stack)")
	public := fs.Bool("public", false, "bind SMTP (25), submission (587), and HTTPS (443) on all interfaces in Docker mode")
	pgPort := fs.Int("pg-port", 5433, "local PostgreSQL port in Docker mode")
	httpsPort := fs.Int("https-port", 0, "Stalwart HTTPS port in Docker mode (default 8443, or 443 with --public)")
	smtpPort := fs.Int("smtp-port", 0, "Stalwart SMTP port in Docker mode (default 2525, or 25 with --public)")
	submissionPort := fs.Int("submission-port", 0, "Stalwart submission port in Docker mode (default 5870, or 587 with --public)")
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
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })

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
	existing, err := readConfig(path)
	if err != nil {
		writef(stderr, "read config: %s\n", err)
		return 1
	}
	if existing.Domain != "" && existing.Domain != *domain {
		writef(stderr, "config already records domain %s\n", existing.Domain)
		return 1
	}

	var (
		result         stalwart.Result
		finalStalwart  string
		finalDBURL     = existing.DatabaseURL
		finalStackDir  = existing.StackDir
		usedDockerMode = !useExternal
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		dockerRes, err := initDockerStack(ctx, dockerInitOptions{
			Domain:         *domain,
			Hostname:       *hostname,
			StackDir:       dir,
			Project:        project,
			Public:         *public,
			PGPort:         *pgPort,
			HTTPSPort:      *httpsPort,
			SMTPPort:       *smtpPort,
			SubmissionPort: *submissionPort,
			RecoveryPort:   *recoveryPort,
			StalwartURL:    *stalwartURL,
			Existing:       existing,
			Progress:       progressOut,
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
	if existing.APIKey != "" {
		cfg.APIKey = existing.APIKey
	}
	if existing.APIURL != "" {
		cfg.APIURL = existing.APIURL
	}
	if err := writeConfig(path, cfg); err != nil {
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
	writef(stdout, "Message retention defaults to %d days.\n", cfg.RetentionDays)
	writef(stdout, "Wrote %s\n", path)
	if usedDockerMode {
		writef(stdout, "Stack files are in %s\n", cfg.StackDir)
		writef(stdout, "Start the server with: inbaid serve\n")
		writef(stdout, "Stop the stack with: docker compose -f %q down\n", filepath.Join(cfg.StackDir, "compose.yaml"))
		return 0
	}
	writef(stdout, "Set STALWART_RECOVERY_MODE=0 and start the stack again before sending mail.\n")
	return 0
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

func readConfig(path string) (config.File, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config.File{}, nil
	}
	if err != nil {
		return config.File{}, err
	}
	var cfg config.File
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return config.File{}, err
	}
	return cfg, nil
}

func writeConfig(path string, cfg config.File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
