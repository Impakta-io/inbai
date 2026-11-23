// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"io"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
	"github.com/Impakta-io/inbai/internal/service"
)

func inboxGenerate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("inbox generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	tlsInsecure := fs.Bool("tls-insecure", false, "skip TLS verification for the Stalwart URL")
	count := fs.Int("count", 0, "number of addresses to create")
	prefix := fs.String("prefix", "u", "local-part prefix")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *count < 1 {
		writef(stderr, "usage: inbai inbox generate --count <n> [--prefix <text>]\n")
		return 2
	}
	cfg, path, client, code := openMail(configPath, tlsInsecure, stderr)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if cfg.DomainID == "" {
		domainID, err := stalwart.LookupDomainID(ctx, client, cfg.Domain)
		if err != nil {
			writef(stderr, "inbox generate: %s\n", err)
			return 1
		}
		cfg.DomainID = domainID
	}
	svc := service.New(stalwart.Mail{Client: client})
	result, err := svc.Generate(ctx, service.Caller{Operator: true}, service.GenerateRequest{
		Prefix:   *prefix,
		Domain:   cfg.Domain,
		DomainID: cfg.DomainID,
		RoleID:   cfg.InboxRoleID,
		Count:    *count,
		Wait:     true,
		Progress: func(done, total int) {
			writef(stdout, "created %d of %d\n", done, total)
		},
	}, "")
	if err != nil {
		writef(stderr, "inbox generate: %s\n", err)
		return 1
	}
	if cfg.Inboxes == nil {
		cfg.Inboxes = map[string]string{}
	}
	for _, inbox := range result.Inboxes {
		cfg.Inboxes[inbox.Address] = inbox.ID
	}
	if err := writeConfig(path, cfg); err != nil {
		writef(stderr, "write config: %s\n", err)
		return 1
	}
	writef(stdout, "Created %d addresses with prefix %s.\n", len(result.Inboxes), strings.ToLower(*prefix))
	return 0
}
