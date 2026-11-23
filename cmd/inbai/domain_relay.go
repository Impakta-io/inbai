// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
)

const (
	operatorPasswordEnv = "INBAI_OPERATOR_PASSWORD"
	relayPasswordEnv    = "INBAI_RELAY_PASSWORD"
)

func domainRelay(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("domain relay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		writef(stderr, "usage: inbai domain relay --host name --user name [--port n] [--config path] [--json]\n")
		writef(stderr, "usage: inbai domain relay --off [--config path] [--json]\n")
		fs.PrintDefaults()
	}
	host := fs.String("host", "", "SMTP relay host name")
	port := fs.Int("port", 587, "SMTP relay port")
	user := fs.String("user", "", "SMTP relay user name")
	off := fs.Bool("off", false, "deliver other domains by MX instead of a relay")
	implicitTLS := fs.Bool("implicit-tls", false, "use TLS from the first byte, as on port 465")
	allowInvalid := fs.Bool("allow-invalid-certs", false, "accept a relay certificate that does not validate")
	configPath := fs.String("config", "", "config file written by inbai init")
	tlsInsecure := fs.Bool("tls-insecure", false, "skip TLS verification for a server that is not on localhost")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(fs.Args()) != 0 {
		writef(stderr, "usage: inbai domain relay --host name --user name [--port n] [--config path] [--json]\n")
		return 2
	}
	if *off && (*host != "" || *user != "") {
		writef(stderr, "domain relay --off does not take --host or --user\n")
		return 2
	}
	if !*off && (*host == "" || *user == "") {
		writef(stderr, "usage: inbai domain relay --host name --user name [--port n] [--config path] [--json]\n")
		return 2
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	password := getenv(operatorPasswordEnv)
	if password == "" {
		password = getenv(recoveryPasswordEnv)
	}
	if password == "" {
		writef(stderr, "domain relay needs %s\n", operatorPasswordEnv)
		return 2
	}
	relayPassword := ""
	if !*off {
		relayPassword = getenv(relayPasswordEnv)
		if relayPassword == "" {
			writef(stderr, "domain relay needs %s\n", relayPasswordEnv)
			return 2
		}
	}
	path, err := configFilePath(*configPath)
	if err != nil {
		writef(stderr, "config directory: %s\n", err)
		return 1
	}
	cfg, err := readConfig(path)
	if err != nil {
		writef(stderr, "read config: %s\n", err)
		return 1
	}
	if cfg.Domain == "" || cfg.StalwartURL == "" {
		writef(stderr, "run inbai init before domain relay\n")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := &stalwart.Client{
		BaseURL:  cfg.StalwartURL,
		User:     "operator@" + cfg.Domain,
		Password: password,
	}
	if *tlsInsecure || loopback(cfg.StalwartURL) {
		client.HTTP = insecureHTTP()
	}
	if *off {
		if err := stalwart.DirectDelivery(ctx, client); err != nil {
			writef(stderr, "domain relay: %s\n", err)
			return 1
		}
		emit(stdout, *asJSON, "Outbound mail for other domains uses their MX records.\n", map[string]any{"mode": "direct"})
		return 0
	}
	if err := stalwart.SetRelay(ctx, client, stalwart.Relay{
		Host:              *host,
		Port:              *port,
		User:              *user,
		Password:          relayPassword,
		ImplicitTLS:       *implicitTLS,
		AllowInvalidCerts: *allowInvalid,
	}); err != nil {
		writef(stderr, "domain relay: %s\n", err)
		return 1
	}
	emit(stdout, *asJSON, fmt.Sprintf("Outbound mail for other domains uses %s:%d as %s.\nLocal domains stay on this server.\n", *host, *port, *user), map[string]any{
		"mode": "relay",
		"host": *host,
		"port": *port,
		"user": *user,
	})
	return 0
}
