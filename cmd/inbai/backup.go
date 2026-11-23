// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"io"
	"os"
	"time"

	"github.com/Impakta-io/inbai/internal/store"
)

func backupCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	return snapshotCmd(args, stdout, stderr, getenv, "backup")
}

func restoreCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	return snapshotCmd(args, stdout, stderr, getenv, "restore")
}

func snapshotCmd(args []string, stdout, stderr io.Writer, getenv func(string) string, name string) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fileFlag := fs.String("file", "", "snapshot file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *fileFlag == "" {
		if name == "restore" {
			writef(stderr, "usage: inbai restore --file path\n")
		} else {
			writef(stderr, "usage: inbai backup --file path\n")
		}
		return 2
	}
	dsn := ""
	if getenv != nil {
		dsn = getenv("INBAI_DATABASE_URL")
	}
	if dsn == "" {
		writef(stderr, "INBAI_DATABASE_URL is required. It is the inbAI database, not the Stalwart database.\n")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		writef(stderr, "%s: %s\n", name, err)
		return 1
	}
	defer db.Close()
	switch name {
	case "backup":
		snap, err := db.Export(ctx)
		if err != nil {
			writef(stderr, "backup: %s\n", err)
			return 1
		}
		raw, err := store.Encode(snap)
		if err != nil {
			writef(stderr, "backup: %s\n", err)
			return 1
		}
		if err := os.WriteFile(*fileFlag, raw, 0o600); err != nil {
			writef(stderr, "backup: %s\n", err)
			return 1
		}
		writef(stdout, "backed up %d events and %d suppressed recipients\n", len(snap.Events), len(snap.Suppressions))
		writef(stdout, "This file has no message bodies. Dump the Stalwart database and its volume as well.\n")
	case "restore":
		raw, err := os.ReadFile(*fileFlag)
		if err != nil {
			writef(stderr, "restore: %s\n", err)
			return 1
		}
		snap, err := store.Decode(raw)
		if err != nil {
			writef(stderr, "restore: %s\n", err)
			return 1
		}
		if err := db.Import(ctx, snap); err != nil {
			writef(stderr, "restore: %s\n", err)
			return 1
		}
		writef(stdout, "restored %d events and %d suppressed recipients\n", len(snap.Events), len(snap.Suppressions))
	default:
		writef(stderr, "unknown command %q\n", name)
		return 2
	}
	return 0
}
