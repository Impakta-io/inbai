// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/api"
	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/config"
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
	backend, err := imap.Open(imapConfig(getenv, stderr))
	if err != nil {
		writef(stderr, "imap: %s\n", strings.TrimPrefix(err.Error(), "imap: "))
		var setting *imap.SettingError
		if os.IsNotExist(err) || errors.As(err, &setting) || strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "empty") {
			return 2
		}
		return 1
	}
	if err := config.CheckSigningKey([]byte(getenv("INBAI_SIGNING_KEY"))); err != nil {
		writef(stderr, "%s\n", err)
		return 2
	}
	imapMode, smtpMode := backend.Security()
	writef(stdout, "imap %s uses %s, smtp %s uses %s\n", getenv("INBAI_IMAP_ADDRESS"), imapMode, getenv("INBAI_SMTP_ADDRESS"), smtpMode)
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
	signingKey, ephemeral, err := resolveSigningKey(ctx, log, []byte(getenv("INBAI_SIGNING_KEY")))
	cancel()
	if err != nil {
		writef(stderr, "signing key: %s\n", err)
		return 1
	}
	if ephemeral {
		writef(stderr, "%s\n", ephemeralKeyNotice)
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

// imapConfig reads the IMAP backend settings. INBAI_IMAP_TLS=1 is the old
// single switch for implicit TLS on both connections. It still means that,
// for each protocol whose own setting is empty, and it is reported as
// deprecated.
func imapConfig(getenv func(string) string, stderr io.Writer) imap.Config {
	cfg := imap.Config{
		IMAP:           getenv("INBAI_IMAP_ADDRESS"),
		SMTP:           getenv("INBAI_SMTP_ADDRESS"),
		IMAPSecurity:   getenv("INBAI_IMAP_SECURITY"),
		SMTPSecurity:   getenv("INBAI_SMTP_SECURITY"),
		AllowPlaintext: getenv("INBAI_ALLOW_PLAINTEXT_AUTH") == "1",
		InsecureTLS:    getenv("INBAI_IMAP_TLS_INSECURE") == "1",
		Accounts:       getenv("INBAI_IMAP_ACCOUNTS"),
		AuthservID:     getenv("INBAI_AUTHSERV_ID"),
	}
	if getenv("INBAI_IMAP_TLS") == "1" {
		writef(stderr, "INBAI_IMAP_TLS=1 is deprecated. It means INBAI_IMAP_SECURITY=tls and INBAI_SMTP_SECURITY=tls where those are unset. Implicit TLS fails on a STARTTLS port such as 587. Set INBAI_IMAP_SECURITY and INBAI_SMTP_SECURITY instead.\n")
		if cfg.IMAPSecurity == "" {
			cfg.IMAPSecurity = string(imap.SecurityTLS)
		}
		if cfg.SMTPSecurity == "" {
			cfg.SMTPSecurity = string(imap.SecurityTLS)
		}
	}
	if cfg.InsecureTLS {
		writef(stderr, "INBAI_IMAP_TLS_INSECURE=1: IMAP and SMTP server certificates are not verified.\n")
	}
	if getenv("INBAI_TLS_INSECURE") != "" {
		writef(stderr, "INBAI_TLS_INSECURE applies to Stalwart mode only and is ignored with INBAI_MAIL=imap. Use INBAI_IMAP_TLS_INSECURE=1 to skip IMAP and SMTP certificate checks.\n")
	}
	return cfg
}

// imapRetentionDays is the server retention period in IMAP mode. The mailboxes
// already exist and hold mail inbAI did not create, so the fallback is zero
// (keep mail). INBAI_RETENTION_DAYS opts in. A period stored with
// inbai retention --days still overrides it.
func imapRetentionDays(getenv func(string) string) (int, error) {
	return parseLimit(getenv, "INBAI_RETENTION_DAYS", 0)
}
