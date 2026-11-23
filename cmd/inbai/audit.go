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

func auditCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		writef(stderr, "usage: inbai audit [--after n] [--limit n] [--config path] [--json]\n")
		fs.PrintDefaults()
	}
	configPath := fs.String("config", "", "config file")
	asJSON := fs.Bool("json", false, "print JSON")
	after := fs.Int64("after", 0, "return actions after this id")
	limit := fs.Int("limit", 0, "page size, from 1 to 100")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	_, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rows, next, err := client.audit(ctx, *after, *limit)
	if err != nil {
		writef(stderr, "audit: %s\n", err)
		return 1
	}
	if *asJSON {
		body := map[string]any{"events": rows}
		if next != 0 {
			body["next"] = next
		}
		emit(stdout, true, "", body)
		return 0
	}
	if len(rows) == 0 {
		writef(stdout, "No audit events.\n")
	}
	for _, row := range rows {
		writef(stdout, "%d\t%s\t%s\t%s\n", row.ID, row.Action, row.Actor, row.Target)
	}
	if next != 0 {
		writef(stdout, "next %s\n", strconv.FormatInt(next, 10))
	}
	return 0
}
