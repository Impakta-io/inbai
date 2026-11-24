// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
)

func loginCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		writef(stderr, "usage: inbai login [--url url] [--client-id id] [--clear] [--config path] [--json]\n")
		fs.PrintDefaults()
	}
	apiURL := fs.String("url", "", "inbaid API URL (default config apiUrl or http://127.0.0.1:8787)")
	clientIDFlag := fs.String("client-id", "", "OAuth client id (uses INBAI_CLIENT_SECRET)")
	clear := fs.Bool("clear", false, "remove stored client credentials from config (operatorToken is kept)")
	configPath := fs.String("config", "", "config file (default inbai/config.json in the user config directory)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		writef(stderr, "usage: inbai login [--url url] [--client-id id] [--clear] [--config path] [--json]\n")
		return 2
	}
	path, err := configFilePath(*configPath)
	if err != nil {
		writef(stderr, "config: %s\n", err)
		return 1
	}
	cfg, err := readConfig(path)
	if err != nil {
		writef(stderr, "read config: %s\n", err)
		return 1
	}
	if *clear {
		cfg.ClientID = ""
		cfg.ClientSecret = ""
		if err := writeConfig(path, cfg); err != nil {
			writef(stderr, "write config: %s\n", err)
			return 1
		}
		emit(stdout, *asJSON, "Cleared stored client credentials. operatorToken is unchanged.\n", map[string]any{"cleared": true, "config": path})
		return 0
	}
	base := strings.TrimSpace(*apiURL)
	if base == "" {
		base = cfg.APIURL
	}
	if base == "" {
		base = config.DefaultAPIURL
	}
	base, ok := checkAPIURL(base)
	if !ok {
		writef(stderr, "login: --url must be an http or https URL\n")
		return 2
	}

	clientID := strings.TrimSpace(*clientIDFlag)
	clientSecret := ""
	operatorToken := ""
	if getenv != nil {
		if clientID == "" {
			clientID = strings.TrimSpace(getenv("INBAI_CLIENT_ID"))
		}
		clientSecret = getenv("INBAI_CLIENT_SECRET")
		operatorToken = getenv("INBAI_OPERATOR_TOKEN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if clientID != "" || clientSecret != "" {
		if clientID == "" || clientSecret == "" {
			writef(stderr, "login: OAuth login needs --client-id (or INBAI_CLIENT_ID) and INBAI_CLIENT_SECRET\n")
			return 2
		}
		if _, err := exchangeToken(ctx, base, clientID, clientSecret); err != nil {
			writef(stderr, "login: %s\n", err)
			return 1
		}
		cfg.APIURL = base
		cfg.ClientID = clientID
		cfg.ClientSecret = clientSecret
		if err := writeConfig(path, cfg); err != nil {
			writef(stderr, "write config: %s\n", err)
			return 1
		}
		emit(stdout, *asJSON, "Logged in to "+base+" as "+clientID+"\n", map[string]any{
			"apiUrl":   base,
			"auth":     "client_credentials",
			"clientId": clientID,
			"config":   path,
		})
		return 0
	}

	if operatorToken != "" {
		if err := config.CheckOperatorToken(operatorToken); err != nil {
			writef(stderr, "login: %s\n", err)
			return 2
		}
		if _, err := (apiClient{base: base, token: operatorToken}).retention(ctx, ""); err != nil {
			writef(stderr, "login: %s\n", err)
			return 1
		}
		cfg.APIURL = base
		cfg.OperatorToken = operatorToken
		cfg.ClientID = ""
		cfg.ClientSecret = ""
		if err := writeConfig(path, cfg); err != nil {
			writef(stderr, "write config: %s\n", err)
			return 1
		}
		emit(stdout, *asJSON, "Logged in to "+base+"\n", map[string]any{
			"apiUrl": base,
			"auth":   "token",
			"config": path,
		})
		return 0
	}

	writef(stderr, "login: set INBAI_OPERATOR_TOKEN, or pass --client-id (or INBAI_CLIENT_ID) with INBAI_CLIENT_SECRET\n")
	return 2
}
