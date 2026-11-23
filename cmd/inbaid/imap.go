// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"net/http"
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
	signingKey := []byte(getenv("INBAI_SIGNING_KEY"))
	if len(signingKey) == 0 {
		signingKey, err = randomKey()
		if err != nil {
			writef(stderr, "signing key: %s\n", err)
			return 1
		}
		writef(stdout, "Signing key, shown once: %x\n", signingKey)
	}
	svc := service.New(backend)
	directory := auth.NewDirectory(signingKey, "http://"+addr)
	if dsn := getenv("INBAI_DATABASE_URL"); dsn != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		log, err := store.Open(ctx, dsn)
		cancel()
		if err != nil {
			writef(stderr, "database: %s\n", err)
			return 1
		}
		svc.SetDB(log)
		directory.SetDB(log)
		feed := events.New(log)
		svc.SetFeed(feed)
		go feed.Run(context.Background(), backend, nil)
	}
	handler := (&api.Server{
		Service:      svc,
		Directory:    directory,
		Token:        getenv("INBAI_OPERATOR_TOKEN"),
		Issuer:       "http://" + addr,
		Domain:       domain,
		DomainID:     "external",
		RoleID:       "external",
		IngestSecret: getenv("INBAI_INGEST_SECRET"),
	}).Handler()
	writef(stdout, "listening on %s\n", addr)
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		writef(stderr, "serve: %s\n", err)
		return 1
	}
	return 0
}
