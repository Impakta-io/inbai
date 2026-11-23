// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"io"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
	"github.com/Impakta-io/inbai/internal/service"
)

func inboxCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "create" {
		writef(stderr, "usage: inbai inbox create <local-part>\n")
		return 2
	}
	fs := flag.NewFlagSet("inbox create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	tlsInsecure := fs.Bool("tls-insecure", false, "skip TLS verification for the Stalwart URL")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		writef(stderr, "usage: inbai inbox create <local-part>\n")
		return 2
	}
	local := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	cfg, path, client, code := openMail(configPath, tlsInsecure, stderr)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	domainID := cfg.DomainID
	if domainID == "" {
		domainID, err := stalwart.LookupDomainID(ctx, client, cfg.Domain)
		if err != nil {
			writef(stderr, "inbox create: %s\n", err)
			return 1
		}
		cfg.DomainID = domainID
	}
	svc := service.New(stalwart.Mail{Client: client})
	id, err := svc.CreateInbox(ctx, service.Caller{Operator: true}, mailbackend.InboxSpec{
		DomainID: cfg.DomainID,
		RoleID:   cfg.InboxRoleID,
		Local:    local,
	})
	if err != nil {
		writef(stderr, "inbox create: %s\n", err)
		return 1
	}
	address := local + "@" + cfg.Domain
	if cfg.Inboxes == nil {
		cfg.Inboxes = map[string]string{}
	}
	cfg.Inboxes[address] = id
	if err := writeConfig(path, cfg); err != nil {
		writef(stderr, "write config: %s\n", err)
		return 1
	}
	writef(stdout, "Created %s\n", address)
	return 0
}

func messageCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writef(stderr, "usage: inbai message [send|list]\n")
		return 2
	}
	switch args[0] {
	case "list":
		return messageList(args[1:], stdout, stderr)
	case "send":
		return messageSend(args[1:], stdout, stderr)
	default:
		writef(stderr, "unknown message command %q\n", args[0])
		return 2
	}
}

func messageList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("message list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	tlsInsecure := fs.Bool("tls-insecure", false, "skip TLS verification for the Stalwart URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		writef(stderr, "usage: inbai message list <address>\n")
		return 2
	}
	cfg, _, client, code := openMail(configPath, tlsInsecure, stderr)
	if code != 0 {
		return code
	}
	accountID, ok := cfg.Inboxes[fs.Arg(0)]
	if !ok {
		writef(stderr, "message list: %s is not in the config. Create it with inbai inbox create.\n", fs.Arg(0))
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	messages, err := service.New(stalwart.Mail{Client: client}).List(ctx, service.Caller{Operator: true}, accountID)
	if err != nil {
		writef(stderr, "message list: %s\n", err)
		return 1
	}
	if len(messages) == 0 {
		writef(stdout, "No messages.\n")
		return 0
	}
	for _, message := range messages {
		writef(stdout, "%s\t%s\n", message.Subject, message.Preview)
	}
	return 0
}

func messageSend(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("message send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	tlsInsecure := fs.Bool("tls-insecure", false, "skip TLS verification for the Stalwart URL")
	from := fs.String("from", "", "sender address")
	to := fs.String("to", "", "recipient address")
	subject := fs.String("subject", "", "subject")
	body := fs.String("body", "", "plain text body")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" || *to == "" || *subject == "" {
		writef(stderr, "usage: inbai message send --from <address> --to <address> --subject <text> [--body <text>]\n")
		return 2
	}
	cfg, _, client, code := openMail(configPath, tlsInsecure, stderr)
	if code != 0 {
		return code
	}
	accountID, ok := cfg.Inboxes[*from]
	if !ok {
		writef(stderr, "message send: %s is not in the config. Create it with inbai inbox create.\n", *from)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := service.New(stalwart.Mail{Client: client}).Deliver(ctx, service.Caller{Operator: true}, mailbackend.Outbound{
		AccountID: accountID,
		From:      *from,
		To:        *to,
		Subject:   *subject,
		Body:      *body,
	})
	if err != nil {
		writef(stderr, "message send: %s\n", err)
		return 1
	}
	writef(stdout, "Sent %s to %s\n", *subject, *to)
	return 0
}

func openMail(configPath *string, tlsInsecure *bool, stderr io.Writer) (config.File, string, *stalwart.Client, int) {
	path, err := configFilePath(*configPath)
	if err != nil {
		writef(stderr, "config: %s\n", err)
		return config.File{}, "", nil, 1
	}
	cfg, err := readConfig(path)
	if err != nil {
		writef(stderr, "read config: %s\n", err)
		return config.File{}, "", nil, 1
	}
	if cfg.APIKey == "" || cfg.StalwartURL == "" || cfg.Domain == "" || cfg.InboxRoleID == "" {
		writef(stderr, "config is missing a service API key. Run inbai init first.\n")
		return config.File{}, "", nil, 2
	}
	client := &stalwart.Client{BaseURL: cfg.StalwartURL, APIKey: cfg.APIKey}
	if *tlsInsecure || strings.HasPrefix(cfg.StalwartURL, "http://") || loopback(cfg.StalwartURL) {
		client.HTTP = insecureHTTP()
	}
	return cfg, path, client, 0
}
