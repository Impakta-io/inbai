// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
)

// lookupDNS is the resolver domain add uses. Tests replace it.
var lookupDNS stalwart.Resolver = net.DefaultResolver

func domainCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		writef(stderr, "usage: inbai domain [add|relay]\n")
		return 2
	}
	switch args[0] {
	case "add":
		return domainAdd(args, stdout, stderr)
	case "relay":
		return domainRelay(args, stdout, stderr, getenv)
	default:
		writef(stderr, "usage: inbai domain [add|relay]\n")
		return 2
	}
}

func domainAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("domain add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	domain := fs.String("domain", "", "domain to publish (default is the domain in the config file)")
	configPath := fs.String("config", "", "config file written by inbai init")
	tlsInsecure := fs.Bool("tls-insecure", false, "skip TLS verification for a server that is not on localhost")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
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
	if cfg.APIKey == "" || cfg.StalwartURL == "" {
		writef(stderr, "run inbai init before domain add\n")
		return 2
	}
	name := *domain
	if name == "" {
		name = cfg.Domain
	}
	if name == "" {
		writef(stderr, "domain add needs --domain\n")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := &stalwart.Client{BaseURL: cfg.StalwartURL, APIKey: cfg.APIKey}
	if *tlsInsecure || loopback(cfg.StalwartURL) {
		client.HTTP = insecureHTTP()
	}
	created := false
	zone, err := stalwart.ZoneFile(ctx, client, name)
	if errors.Is(err, stalwart.ErrDomainNotConfigured) {
		if err = stalwart.CreateDomain(ctx, client, name); err == nil {
			created = true
			zone, err = stalwart.ZoneFile(ctx, client, name)
		}
	}
	if err != nil {
		writef(stderr, "domain add: %s\n", err)
		return 1
	}
	records, err := stalwart.ParseZone(zone)
	if err != nil {
		writef(stderr, "domain add: %s\n", err)
		return 1
	}
	if !strings.HasSuffix(zone, "\n") {
		zone += "\n"
	}
	findings := stalwart.Check(ctx, records, lookupDNS)
	if *asJSON {
		checks := make([]map[string]string, len(findings))
		for i, finding := range findings {
			checks[i] = map[string]string{"name": finding.Name, "type": finding.Type, "status": finding.Status}
		}
		emit(stdout, true, "", map[string]any{"zone": zone, "created": created, "checks": checks})
		return 0
	}
	if created {
		writef(stdout, "Created domain %s in Stalwart.\n\n", name)
	}
	writef(stdout, "%s\n", zone)
	for _, finding := range findings {
		writef(stdout, "%s %s: %s\n", finding.Name, finding.Type, finding.Status)
	}
	return 0
}

func loopback(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func insecureHTTP() *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // Stalwart's first certificate is self-signed.
	}}
}
