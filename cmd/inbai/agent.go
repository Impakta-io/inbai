// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"io"
	"time"
)

func agentCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		writef(stderr, "usage: inbai agent [create|grant|suspend|revoke]\n")
		return 2
	}
	switch args[0] {
	case "create":
		return agentCreate(args[1:], stdout, stderr, getenv)
	case "grant":
		return agentGrant(args[1:], stdout, stderr, getenv)
	case "suspend":
		return agentSuspend(args[1:], stdout, stderr, getenv)
	case "revoke":
		return agentRevoke(args[1:], stdout, stderr, getenv)
	default:
		writef(stderr, "unknown agent command %q\n", args[0])
		return 2
	}
}

func agentCreate(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("agent create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		writef(stderr, "usage: inbai agent create <name>\n")
		return 2
	}
	_, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	id, secret, err := client.createAgent(ctx, fs.Arg(0))
	if err != nil {
		writef(stderr, "agent create: %s\n", err)
		return 1
	}
	if *asJSON {
		emit(stdout, true, "", map[string]string{"id": id, "secret": secret, "agent": fs.Arg(0)})
		return 0
	}
	writef(stdout, "Agent %s client id %s\n", fs.Arg(0), id)
	writef(stdout, "Secret, shown once: %s\n", secret)
	return 0
}

func agentGrant(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	return agentGrantOrRevoke(args, stdout, stderr, getenv, false)
}

func agentRevoke(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	return agentGrantOrRevoke(args, stdout, stderr, getenv, true)
}

func agentGrantOrRevoke(args []string, stdout, stderr io.Writer, getenv func(string) string, revoke bool) int {
	name := "grant"
	if revoke {
		name = "revoke"
	}
	fs := flag.NewFlagSet("agent "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 3 {
		if revoke {
			writef(stderr, "usage: inbai agent revoke <agent> <inbox> <permission>\n")
		} else {
			writef(stderr, "usage: inbai agent grant <agent> <inbox> <permission>\n")
		}
		return 2
	}
	_, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var err error
	if revoke {
		err = client.revoke(ctx, fs.Arg(0), fs.Arg(1), fs.Arg(2))
	} else {
		err = client.grant(ctx, fs.Arg(0), fs.Arg(1), fs.Arg(2))
	}
	if err != nil {
		writef(stderr, "agent %s: %s\n", name, err)
		return 1
	}
	body := map[string]string{"agent": fs.Arg(0), "inbox": fs.Arg(1), "permission": fs.Arg(2)}
	if *asJSON {
		emit(stdout, true, "", body)
		return 0
	}
	if revoke {
		writef(stdout, "Revoked %s on %s from %s\n", fs.Arg(2), fs.Arg(1), fs.Arg(0))
		return 0
	}
	writef(stdout, "Granted %s on %s to %s\n", fs.Arg(2), fs.Arg(1), fs.Arg(0))
	return 0
}

func agentSuspend(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("agent suspend", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		writef(stderr, "usage: inbai agent suspend <name>\n")
		return 2
	}
	_, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := client.suspendAgent(ctx, fs.Arg(0)); err != nil {
		writef(stderr, "agent suspend: %s\n", err)
		return 1
	}
	if *asJSON {
		emit(stdout, true, "", map[string]string{"suspended": fs.Arg(0)})
		return 0
	}
	writef(stdout, "Suspended %s\n", fs.Arg(0))
	return 0
}
