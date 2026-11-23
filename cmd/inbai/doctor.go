// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/Impakta-io/inbai/internal/doctor"
	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
)

// doctorProbe is the network access inbai doctor uses. Tests replace it.
var doctorProbe doctor.Probe = doctor.Live()

func doctorCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	domain := fs.String("domain", "", "domain to check (default is the domain in the config file)")
	configPath := fs.String("config", "", "config file written by inbai init")
	tlsInsecure := fs.Bool("tls-insecure", false, "skip TLS verification for a server that is not on localhost")
	if err := fs.Parse(args); err != nil {
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
		writef(stderr, "run inbai init before doctor\n")
		return 2
	}
	name := *domain
	if name == "" {
		name = cfg.Domain
	}
	if name == "" {
		writef(stderr, "doctor needs --domain\n")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := &stalwart.Client{BaseURL: cfg.StalwartURL, APIKey: cfg.APIKey}
	if *tlsInsecure || loopback(cfg.StalwartURL) {
		client.HTTP = insecureHTTP()
	}
	zone, err := stalwart.ZoneFile(ctx, client, name)
	if err != nil {
		writef(stderr, "doctor: %s\n", err)
		return 1
	}
	parsed, err := stalwart.ParseZone(zone)
	if err != nil {
		writef(stderr, "doctor: %s\n", err)
		return 1
	}
	records := make([]doctor.Record, len(parsed))
	for i, record := range parsed {
		records[i] = doctor.Record{Name: record.Name, Type: record.Type, Value: record.Value}
	}
	lines := doctor.Run(ctx, records, cfg.Hostname, doctorProbe)
	for _, line := range lines {
		writef(stdout, "%s\n", line.Text)
	}
	if !doctor.Healthy(lines) {
		return 1
	}
	return 0
}
