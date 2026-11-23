// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

// Command load measures one run against a running inbaid and prints the result.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/Impakta-io/inbai/internal/load"
)

func main() {
	urlFlag := flag.String("url", "http://127.0.0.1:8787", "inbaid base URL")
	tokenFlag := flag.String("token", os.Getenv("INBAI_OPERATOR_TOKEN"), "operator bearer token")
	prefixFlag := flag.String("prefix", "load", "local-part prefix")
	inboxesFlag := flag.Int("inboxes", 10, "addresses to provision")
	messagesFlag := flag.Int("messages", 1, "messages to send to each address")
	concurrencyFlag := flag.Int("concurrency", 4, "calls in flight")
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *tokenFlag == "" {
		writef(os.Stderr, "INBAI_OPERATOR_TOKEN or -token is required\n")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	report, err := load.Run(ctx, load.Options{
		BaseURL: *urlFlag, Token: *tokenFlag, Prefix: *prefixFlag,
		Inboxes: *inboxesFlag, Messages: *messagesFlag, Concurrency: *concurrencyFlag,
		Client: &http.Client{Timeout: 30 * time.Second},
	})
	if err != nil {
		writef(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		writef(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
	if report.Errors > 0 {
		os.Exit(1)
	}
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
