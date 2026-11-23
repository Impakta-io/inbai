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
	token := getenv("INBAI_OPERATOR_TOKEN")
	if token == "" {
		writef(stderr, "INBAI_OPERATOR_TOKEN is required\n")
		return 2
	}
	if getenv("INBAI_MAIL") == "imap" {
		return serveIMAP(*addr, stdout, stderr, getenv)
	}
	path, err := configPathOf(*configPath)
	if err != nil {
		writef(stderr, "config: %s\n", err)
		return 1
	}
	cfg, err := readConfig(path)
	if err != nil {
		writef(stderr, "read config: %s\n", err)
		return 1
	}
	if cfg.APIKey == "" || cfg.StalwartURL == "" || cfg.Domain == "" || cfg.InboxRoleID == "" {
		writef(stderr, "config is missing a service API key. Run inbai init first.\n")
		return 2
	}
	client := &stalwart.Client{BaseURL: cfg.StalwartURL, APIKey: cfg.APIKey}
	if strings.HasPrefix(cfg.StalwartURL, "http://") || loopback(cfg.StalwartURL) {
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
	signingKey := []byte(getenv("INBAI_SIGNING_KEY"))
	if len(signingKey) == 0 {
		var err error
		signingKey, err = randomKey()
		if err != nil {
			writef(stderr, "signing key: %s\n", err)
			return 1
		}
		writef(stdout, "Signing key, shown once: %x\n", signingKey)
	}
	svc := service.New(stalwart.Mail{Client: client})
	if dsn := getenv("INBAI_DATABASE_URL"); dsn != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		log, err := store.Open(ctx, dsn)
		cancel()
		if err != nil {
			writef(stderr, "database: %s\n", err)
			return 1
		}
		feed := events.New(log)
		svc.SetFeed(feed)
		go feed.Run(context.Background(), stalwart.Mail{Client: client}, nil)
	}
	issuer := "http://" + *addr
	handler := (&api.Server{
		Service:      svc,
		Directory:    auth.NewDirectory(signingKey, issuer),
		Token:        token,
		Issuer:       issuer,
		Domain:       cfg.Domain,
		DomainID:     cfg.DomainID,
		RoleID:       cfg.InboxRoleID,
		IngestSecret: getenv("INBAI_INGEST_SECRET"),
	}).Handler()
	writef(stdout, "listening on %s\n", *addr)
	server := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		writef(stderr, "serve: %s\n", err)
		return 1
	}
	return 0
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
