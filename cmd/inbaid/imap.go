// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/api"
	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mailbackend/imap"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
)

func serveIMAP(addr string, stdout, stderr io.Writer, getenv func(string) string) int {
	domain := getenv("INBAI_DOMAIN")
	if domain == "" {
		writef(stderr, "INBAI_DOMAIN is required\n")
		return 2
	}
	retentionDays, err := imapRetentionDays(getenv)
	if err != nil {
		writef(stderr, "retention: %s\n", err)
		return 2
	}
	backend, err := imap.Open(imap.Config{
		IMAP:     getenv("INBAI_IMAP_ADDRESS"),
		SMTP:     getenv("INBAI_SMTP_ADDRESS"),
		TLS:      getenv("INBAI_IMAP_TLS") == "1",
		Accounts: getenv("INBAI_IMAP_ACCOUNTS"),
	})
	if err != nil {
		writef(stderr, "imap: %s\n", err)
		if os.IsNotExist(err) || strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "empty") {
			return 2
		}
		return 1
	}
	var log *store.Postgres
	if dsn := getenv("INBAI_DATABASE_URL"); dsn != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		log, err = store.Open(ctx, dsn)
		cancel()
		if err != nil {
			writef(stderr, "database: %s\n", err)
			return 1
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	signingKey, showKey, err := resolveSigningKey(ctx, log, []byte(getenv("INBAI_SIGNING_KEY")))
	cancel()
	if err != nil {
		writef(stderr, "signing key: %s\n", err)
		return 1
	}
	if showKey {
		writef(stdout, "Signing key, shown once: %x\n", signingKey)
	}
	svc := service.New(backend)
	if err := applySendLimits(svc, getenv); err != nil {
		writef(stderr, "send limits: %s\n", err)
		return 2
	}
	directory := auth.NewDirectory(signingKey, "http://"+addr)
	svc.SetSuspensions(directory)
	if log != nil {
		svc.SetDB(log)
		directory.SetDB(log)
		feed := events.New(log)
		svc.SetFeed(feed)
		if err := applyWebhookPolicy(feed, getenv); err != nil {
			writef(stderr, "webhooks: %s\n", err)
			return 2
		}
		go feed.RunWebhooks(context.Background(), nil)
		go newWorker(log, svc, feed, backend, retentionDays, stderr).Run(context.Background())
	} else {
		go retainLoop(svc, retentionDays, stderr)
	}
	handler := (&api.Server{
		Service:       svc,
		Directory:     directory,
		Token:         getenv("INBAI_OPERATOR_TOKEN"),
		Issuer:        "http://" + addr,
		Domain:        domain,
		DomainID:      "external",
		RoleID:        "external",
		IngestSecret:  getenv("INBAI_INGEST_SECRET"),
		RetentionDays: retentionDays,
		Log:           stderr,
	}).Handler()
	writef(stdout, "listening on %s\n", addr)
	if err := newHTTPServer(addr, handler).ListenAndServe(); err != nil {
		writef(stderr, "serve: %s\n", err)
		return 1
	}
	return 0
}

// imapRetentionDays is the server retention period in IMAP mode. The mailboxes
// already exist and hold mail inbAI did not create, so the fallback is zero
// (keep mail). INBAI_RETENTION_DAYS opts in. A period stored with
// inbai retention --days still overrides it.
func imapRetentionDays(getenv func(string) string) (int, error) {
	return parseLimit(getenv, "INBAI_RETENTION_DAYS", 0)
}
