// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"io"
	"time"
)

func inboxAllowlist(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		writef(stderr, "usage: inbai inbox allowlist [get|set|clear]\n")
		return 2
	}
	switch args[0] {
	case "get":
		return inboxAllowlistGet(args[1:], stdout, stderr, getenv)
	case "set":
		return inboxAllowlistSet(args[1:], stdout, stderr, getenv)
	case "clear":
		return inboxAllowlistClear(args[1:], stdout, stderr, getenv)
	default:
		writef(stderr, "usage: inbai inbox allowlist [get|set|clear]\n")
		return 2
	}
}

func inboxAllowlistGet(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("inbox allowlist get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		writef(stderr, "usage: inbai inbox allowlist get [--config path] [--json] <inbox-id>\n")
		return 2
	}
	_, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	body, err := client.allowlist(ctx, fs.Arg(0))
	if err != nil {
		writef(stderr, "inbox allowlist: %s\n", err)
		return 1
	}
	if *asJSON {
		emit(stdout, true, "", body)
		return 0
	}
	if !body.Set || len(body.Senders) == 0 {
		writef(stdout, "No sender allowlist.\n")
		return 0
	}
	for _, sender := range body.Senders {
		writef(stdout, "%s\n", sender)
	}
	return 0
}

func inboxAllowlistSet(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("inbox allowlist set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 2 {
		writef(stderr, "usage: inbai inbox allowlist set [--config path] [--json] <inbox-id> <sender> [<sender>...]\n")
		return 2
	}
	return writeAllowlist(fs.Arg(0), fs.Args()[1:], *configPath, *asJSON, stdout, stderr, getenv)
}

func inboxAllowlistClear(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("inbox allowlist clear", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		writef(stderr, "usage: inbai inbox allowlist clear [--config path] [--json] <inbox-id>\n")
		return 2
	}
	return writeAllowlist(fs.Arg(0), []string{}, *configPath, *asJSON, stdout, stderr, getenv)
}

func writeAllowlist(inbox string, senders []string, configPath string, asJSON bool, stdout, stderr io.Writer, getenv func(string) string) int {
	_, _, client, code := openAPI(&configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	body, err := client.setAllowlist(ctx, inbox, senders)
	if err != nil {
		writef(stderr, "inbox allowlist: %s\n", err)
		return 1
	}
	if asJSON {
		emit(stdout, true, "", body)
		return 0
	}
	if !body.Set || len(body.Senders) == 0 {
		writef(stdout, "No sender allowlist.\n")
		return 0
	}
	if len(body.Senders) == 1 {
		writef(stdout, "Sender allowlist has 1 entry.\n")
		return 0
	}
	writef(stdout, "Sender allowlist has %d entries.\n", len(body.Senders))
	return 0
}
