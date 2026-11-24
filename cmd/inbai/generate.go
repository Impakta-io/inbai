// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"io"
	"strings"
	"time"
)

func inboxGenerate(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("inbox generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	asJSON := fs.Bool("json", false, "print JSON")
	count := fs.Int("count", 0, "number of addresses to create")
	prefix := fs.String("prefix", "u", "local-part prefix")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *count < 1 {
		writef(stderr, "usage: inbai inbox generate --count <n> [--prefix <text>]\n")
		return 2
	}
	cfg, path, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	var last int
	res, err := client.generate(ctx, *prefix, *count, func(done, total int) {
		if *asJSON || done == last {
			return
		}
		last = done
		writef(stdout, "created %d of %d\n", done, total)
	})
	if err != nil {
		writef(stderr, "inbox generate: %s\n", err)
		return 1
	}
	if cfg.Inboxes == nil {
		cfg.Inboxes = map[string]string{}
	}
	for _, inbox := range res.Inboxes {
		cfg.Inboxes[inbox.Address] = inbox.ID
	}
	if err := writeConfig(path, cfg); err != nil {
		writef(stderr, "write config: %s\n", err)
		return 1
	}
	existing := res.Existing
	if existing == nil {
		existing = []string{}
	}
	if *asJSON {
		emit(stdout, true, "", map[string]any{"inboxes": res.Inboxes, "existing": existing})
		return 0
	}
	writef(stdout, "Created %d addresses with prefix %s.\n", len(res.Inboxes), strings.ToLower(*prefix))
	if n := len(existing); n > 0 {
		if n <= 10 {
			writef(stdout, "Skipped %d that already exist: %s.\n", n, strings.Join(existing, ", "))
		} else {
			writef(stdout, "Skipped %d that already exist.\n", n)
		}
	}
	return 0
}
