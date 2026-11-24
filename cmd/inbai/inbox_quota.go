// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"
)

func inboxQuotaGet(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("inbox quota get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		writef(stderr, "usage: inbai inbox quota get [--config path] [--json] <inbox-id>\n")
		return 2
	}
	cfg, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	inbox := fs.Arg(0)
	if _, id, ok := resolveInbox(cfg, inbox); ok {
		inbox = id
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	q, err := client.storageQuota(ctx, inbox)
	if err != nil {
		writef(stderr, "inbox quota: %s\n", err)
		return 1
	}
	emit(stdout, *asJSON, formatStorageQuota(q), q)
	return 0
}

func inboxQuotaSet(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("inbox quota set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		writef(stderr, "usage: inbai inbox quota set [--config path] [--json] <inbox-id> <limit-bytes>\n")
		return 2
	}
	limitBytes, err := strconv.ParseInt(fs.Arg(1), 10, 64)
	if err != nil || limitBytes < 0 {
		writef(stderr, "inbox quota: limit-bytes must be zero or greater\n")
		return 2
	}
	cfg, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	inbox := fs.Arg(0)
	if _, id, ok := resolveInbox(cfg, inbox); ok {
		inbox = id
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	q, err := client.setStorageQuota(ctx, inbox, limitBytes)
	if err != nil {
		writef(stderr, "inbox quota: %s\n", err)
		return 1
	}
	emit(stdout, *asJSON, formatStorageQuota(q), q)
	return 0
}

func formatStorageQuota(q apiStorageQuota) string {
	if q.LimitBytes == 0 {
		return fmt.Sprintf("Storage quota: unlimited (%d bytes used)\n", q.UsedBytes)
	}
	return fmt.Sprintf("Storage quota: %d / %d bytes\n", q.UsedBytes, q.LimitBytes)
}
