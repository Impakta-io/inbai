// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"io"
	"strconv"
	"time"
)

func retentionCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) > 0 && args[0] == "run" {
		return retentionRun(args[1:], stdout, stderr, getenv)
	}
	fs := flag.NewFlagSet("retention", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		writef(stderr, "usage: inbai retention [--days n] [--inbox id] [--config path] [--json]\n")
		fs.PrintDefaults()
	}
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	days := fs.Int("days", -1, "days to keep mail, or 0 to keep it")
	inbox := fs.String("inbox", "", "inbox id, or empty for the server default")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, path, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if *days >= 0 {
		got, err := client.setRetention(ctx, *inbox, *days)
		if err != nil {
			writef(stderr, "retention: %s\n", err)
			return 1
		}
		if *inbox == "" {
			cfg.RetentionDays = *days
			if err := writeConfig(path, cfg); err != nil {
				writef(stderr, "retention: %s\n", err)
				return 1
			}
		}
		return printRetention(stdout, *asJSON, got)
	}
	got, err := client.retention(ctx, *inbox)
	if err != nil {
		writef(stderr, "retention: %s\n", err)
		return 1
	}
	return printRetention(stdout, *asJSON, got)
}

func retentionRun(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("retention run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		writef(stderr, "usage: inbai retention run [--config path] [--json]\n")
		fs.PrintDefaults()
	}
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	_, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	deleted, err := client.runRetention(ctx)
	if err != nil {
		writef(stderr, "retention run: %s\n", err)
		return 1
	}
	emit(stdout, *asJSON, "Deleted "+strconv.Itoa(deleted)+" messages.\n", map[string]int{"deleted": deleted})
	return 0
}

func printRetention(stdout io.Writer, asJSON bool, got apiRetention) int {
	if asJSON {
		emit(stdout, true, "", got)
		return 0
	}
	if got.Inbox == "" {
		writef(stdout, "Server retention is %d days.\n", got.Days)
		return 0
	}
	writef(stdout, "Inbox %s retention is %d days.\n", got.Inbox, got.Days)
	return 0
}
