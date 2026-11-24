// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/api"
	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/config"
	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
	"github.com/Impakta-io/inbai/internal/version"
)

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 || args[0] == "version" || args[0] == "-h" || args[0] == "--help" {
		writef(stdout, "%s\n", version.String(version.Server))
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			writef(stdout, "usage: inbaid [version|serve]\n")
		}
		return 0
	}
	switch args[0] {
	case "serve":
		return serve(args[1:], stdout, stderr, getenv)
	default:
		writef(stderr, "unknown command %q\n", args[0])
		return 2
	}
}

func serve(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	addr := fs.String("addr", "127.0.0.1:8787", "listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if getenv != nil && getenv("INBAI_MAIL") == "imap" {
		token := strings.TrimSpace(getenv("INBAI_OPERATOR_TOKEN"))
		if token == "" {
			writef(stderr, "INBAI_OPERATOR_TOKEN is required\n")
			return 2
		}
		if err := config.CheckOperatorToken(token); err != nil {
			writef(stderr, "%s\n", err)
			return 2
		}
		return serveIMAP(*addr, stdout, stderr, getenv)
	}
	path, err := configPathOf(*configPath)
	if err != nil {
		writef(stderr, "config: %s\n", err)
		return 1
	}
	cfg, cfgErr := readConfig(path)
	token := resolveToken(cfg, getenv)
	if token == "" {
		writef(stderr, "INBAI_OPERATOR_TOKEN is required\n")
		return 2
	}
	if err := config.CheckOperatorToken(token); err != nil {
		writef(stderr, "%s\n", err)
		return 2
	}
	if err := config.CheckSigningKey([]byte(getenv("INBAI_SIGNING_KEY"))); err != nil {
		writef(stderr, "%s\n", err)
		return 2
	}
	if cfgErr != nil {
		writef(stderr, "read config: %s\n", cfgErr)
		return 1
	}
	if cfg.APIKey == "" || cfg.StalwartURL == "" || cfg.Domain == "" || cfg.InboxRoleID == "" {
		writef(stderr, "config is missing a service API key. Run inbai init first.\n")
		return 2
	}
	cfg, insecure := serverMail(cfg, getenv)
	client := &stalwart.Client{BaseURL: cfg.StalwartURL, APIKey: cfg.APIKey}
	if insecure {
		client.HTTP = insecureHTTP()
	}
	if cfg.DomainID == "" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		cfg.DomainID, err = stalwart.LookupDomainID(ctx, client, cfg.Domain)
		cancel()
		if err != nil {
			writef(stderr, "domain: %s\n", err)
			return 1
		}
	}
	var log *store.Postgres
	if dsn := resolveDatabaseURL(cfg, getenv); dsn != "" {
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
	svc := service.New(stalwart.Mail{Client: client})
	if err := applySendLimits(svc, getenv); err != nil {
		writef(stderr, "send limits: %s\n", err)
		return 2
	}
	issuer := "http://" + *addr
	directory := auth.NewDirectory(signingKey, issuer)
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
		go newWorker(log, svc, feed, stalwart.Mail{Client: client}, cfg.RetentionDays, stderr).Run(context.Background())
	} else {
		go retainLoop(svc, cfg.RetentionDays, stderr)
	}
	handler := (&api.Server{
		Service:       svc,
		Directory:     directory,
		Token:         token,
		Issuer:        issuer,
		Domain:        cfg.Domain,
		DomainID:      cfg.DomainID,
		RoleID:        cfg.InboxRoleID,
		IngestSecret:  getenv("INBAI_INGEST_SECRET"),
		RetentionDays: cfg.RetentionDays,
		Log:           stderr,
	}).Handler()
	writef(stdout, "listening on %s\n", *addr)
	if err := newHTTPServer(*addr, handler).ListenAndServe(); err != nil {
		writef(stderr, "serve: %s\n", err)
		return 1
	}
	return 0
}

// newHTTPServer is the listener for both mail modes.
//
// ReadTimeout bounds reading a whole request, body included, so a client cannot
// hold a connection by sending slowly. Go also cancels the request context when
// it passes while the handler runs, so it must exceed the longest handler: a
// 25 second long poll on GET /v1/events or an MCP wait. Two minutes also lets
// a 16 MiB JSON upload arrive at about 1 Mbit/s.
//
// WriteTimeout runs from the end of the request headers to the end of the
// response. It is longer than ReadTimeout so a 25 MB attachment download to a
// slow client still finishes, and a client that stops reading is let go.
//
// IdleTimeout closes a keep-alive connection that sends no next request.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       time.Minute,
	}
}

func retainLoop(svc *service.Service, days int, stderr io.Writer) {
	sweep := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if _, err := svc.Sweep(ctx, service.Caller{Operator: true}, days); err != nil {
			writef(stderr, "retention: %s\n", err)
		}
	}
	sweep()
	ticker := time.NewTicker(time.Hour)
	for range ticker.C {
		sweep()
	}
}

func resolveToken(cfg config.File, getenv func(string) string) string {
	if getenv != nil {
		if token := strings.TrimSpace(getenv("INBAI_OPERATOR_TOKEN")); token != "" {
			return token
		}
	}
	return strings.TrimSpace(cfg.OperatorToken)
}

func resolveDatabaseURL(cfg config.File, getenv func(string) string) string {
	if getenv != nil {
		if dsn := strings.TrimSpace(getenv("INBAI_DATABASE_URL")); dsn != "" {
			return dsn
		}
	}
	return strings.TrimSpace(cfg.DatabaseURL)
}

func serverMail(cfg config.File, getenv func(string) string) (config.File, bool) {
	if getenv != nil {
		if raw := strings.TrimSpace(getenv("INBAI_STALWART_URL")); raw != "" {
			cfg.StalwartURL = raw
		}
		if getenv("INBAI_TLS_INSECURE") == "1" {
			return cfg, true
		}
	}
	if strings.HasPrefix(cfg.StalwartURL, "http://") || loopback(cfg.StalwartURL) {
		return cfg, true
	}
	return cfg, false
}

func configPathOf(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "inbai", "config.json"), nil
}

func readConfig(path string) (config.File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return config.File{}, err
	}
	var cfg config.File
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return config.File{}, err
	}
	return cfg, nil
}

func loopback(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func insecureHTTP() *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // Stalwart's first certificate is self-signed.
	}}
}

func randomKey() ([]byte, error) {
	buf := make([]byte, 32)
	_, err := rand.Read(buf)
	return buf, err
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
