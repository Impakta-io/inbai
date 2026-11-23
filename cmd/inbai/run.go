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
	case "domain":
		return domainCmd(args[1:], stdout, stderr)
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
		writef(stdout, "usage: inbai [version|init|domain|doctor|inbox|message|agent|audit|retention|mcp|backup|restore]\n")
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
		writef(stderr, "usage: inbai init --domain name [--url url] [--hostname name] [--stalwart-url url] [--config path] [--json]\n")
		fs.PrintDefaults()
	}
	url := fs.String("url", "http://127.0.0.1:8080", "Stalwart recovery API, reachable on the host")
	domain := fs.String("domain", "", "DNS domain to create")
	hostname := fs.String("hostname", "", "mail hostname (default mail.<domain>)")
	stalwartURL := fs.String("stalwart-url", "https://127.0.0.1", "Stalwart URL to store for later commands")
	configPath := fs.String("config", "", "config file to write (default is the user config directory)")
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
	password := ""
	if getenv != nil {
		password = getenv(recoveryPasswordEnv)
	}
	if password == "" {
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := stalwart.Bootstrap(ctx, &stalwart.Client{
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
	cfg := config.File{
		Domain:            *domain,
		Hostname:          result.Hostname,
		StalwartURL:       *stalwartURL,
		ServiceAccountID:  result.ServiceAccountID,
		OperatorAccountID: result.OperatorAccountID,
		InboxRoleID:       result.InboxRoleID,
		ServiceRoleID:     result.ServiceRoleID,
		DomainID:          result.DomainID,
		Inboxes:           existing.Inboxes,
		APIKey:            result.APIKey,
		APIURL:            config.DefaultAPIURL,
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
		emit(stdout, true, "", map[string]any{
			"domain":         cfg.Domain,
			"hostname":       cfg.Hostname,
			"operator":       "operator@" + cfg.Domain,
			"serviceAccount": "service@" + cfg.Domain,
			"retentionDays":  cfg.RetentionDays,
			"config":         path,
			"next":           "Set STALWART_RECOVERY_MODE=0 and start the stack again before sending mail.",
		})
		return 0
	}
	writef(stdout, "Domain %s is ready.\n", cfg.Domain)
	writef(stdout, "Operator login is operator@%s.\n", cfg.Domain)
	writef(stdout, "Service account is service@%s.\n", cfg.Domain)
	writef(stdout, "Message retention defaults to %d days.\n", cfg.RetentionDays)
	writef(stdout, "Wrote %s\n", path)
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
