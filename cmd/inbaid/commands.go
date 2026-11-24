// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"

	"github.com/Impakta-io/inbai/cmd/internal/cli"
	"github.com/Impakta-io/inbai/internal/version"
)

// program is the inbaid command table. Dispatch, help, and
// docs/reference/cli.md all read it. A new command needs a row here.
var program = &cli.Program{
	Name:    "inbaid",
	Summary: "Server for inbAI, self-hosted email for AI agents: the REST API, MCP, and workers.",
	Sections: []cli.Section{
		{Title: "Commands", Names: []string{"serve", "version", "help"}},
	},
	Footer: `Configuration:
  inbaid serve reads inbai/config.json, the file inbai init writes, unless
  INBAI_MAIL=imap. Run 'inbaid help serve' for its environment variables.
`,
	DocsURL:  "https://github.com/Impakta-io/inbai/tree/main/docs",
	Commands: commands,
}

var commands = []cli.Command{
	{
		Name:    "serve",
		Summary: "Run the REST API, MCP, and background workers",
		Usage:   []string{"inbaid serve [--addr host:port] [--config path]"},
		Description: "In Stalwart mode, the default, serve reads the config file inbai init wrote. " +
			"With INBAI_MAIL=imap it reads only the environment, needs INBAI_OPERATOR_TOKEN, and uses an existing IMAP and SMTP server.\n\n" +
			"Without a database, jobs, quota counters, and the signing key live in this process only. " +
			"Environment variables override the config file. A bad value stops startup with exit code 2.",
		Env: []cli.EnvVar{
			{Group: "Core", Name: "INBAI_OPERATOR_TOKEN", Text: "operator token", Default: "operatorToken in config"},
			{Group: "Core", Name: "INBAI_SIGNING_KEY", Text: "signing key, 32+ bytes", Default: "stored, or random"},
			{Group: "Core", Name: "INBAI_INGEST_SECRET", Text: "secret Stalwart sends to /v1/hooks/ingest"},
			{Group: "Core", Name: "INBAI_STALWART_URL", Text: "Stalwart URL", Default: "stalwartUrl in config"},
			{Group: "Core", Name: "INBAI_TLS_INSECURE", Text: "1 skips Stalwart certificate checks"},
			{Group: "Database", Name: "INBAI_DATABASE_URL", Text: "PostgreSQL URL", Default: "databaseUrl in config"},
			{Group: "Mail mode", Name: "INBAI_MAIL", Text: "imap uses IMAP and SMTP instead of Stalwart"},
			{Group: "IMAP mode", Name: "INBAI_DOMAIN", Text: "mail domain (required)"},
			{Group: "IMAP mode", Name: "INBAI_IMAP_ADDRESS", Text: "IMAP host:port (required)"},
			{Group: "IMAP mode", Name: "INBAI_SMTP_ADDRESS", Text: "SMTP host:port (required)"},
			{Group: "IMAP mode", Name: "INBAI_IMAP_ACCOUNTS", Text: "account file, JSON (required)"},
			{Group: "IMAP mode", Name: "INBAI_IMAP_SECURITY", Text: "tls, starttls, or none", Default: "by port"},
			{Group: "IMAP mode", Name: "INBAI_SMTP_SECURITY", Text: "tls, starttls, or none", Default: "by port"},
			{Group: "IMAP mode", Name: "INBAI_ALLOW_PLAINTEXT_AUTH", Text: "1 allows none to a remote host"},
			{Group: "IMAP mode", Name: "INBAI_IMAP_TLS_INSECURE", Text: "1 skips IMAP and SMTP certificate checks"},
			{Group: "IMAP mode", Name: "INBAI_AUTHSERV_ID", Text: "authserv-id of trusted Authentication-Results"},
			{Group: "IMAP mode", Name: "INBAI_IMAP_TLS", Text: "deprecated; 1 means tls for both"},
			{Group: "Limits and quotas", Name: "INBAI_QUOTA_INBOX_DAILY", Text: "daily sends per inbox", Default: "50"},
			{Group: "Limits and quotas", Name: "INBAI_QUOTA_DOMAIN_DAILY", Text: "daily sends per sender domain", Default: "500"},
			{Group: "Limits and quotas", Name: "INBAI_QUOTA_WORKSPACE_DAILY", Text: "daily sends per workspace", Default: "5000"},
			{Group: "Limits and quotas", Name: "INBAI_RATE_RECIPIENT_PER_MINUTE", Text: "per recipient domain", Default: "30"},
			{Group: "Limits and quotas", Name: "INBAI_STORAGE_QUOTA_BYTES", Text: "storage per new inbox", Default: "104857600"},
			{Group: "Webhooks", Name: "INBAI_WEBHOOK_WORKERS", Text: "concurrent posts, 1 to 256", Default: "16"},
			{Group: "Webhooks", Name: "INBAI_WEBHOOK_ALLOW_PRIVATE", Text: "true allows http and private addresses"},
			{Group: "Retention", Name: "INBAI_EVENT_RETENTION_DAYS", Text: "days to keep events, 0 keeps", Default: "30"},
			{Group: "Retention", Name: "INBAI_DELIVERY_RETENTION_DAYS", Text: "days to keep deliveries", Default: "7"},
			{Group: "Retention", Name: "INBAI_RETENTION_DAYS", Text: "IMAP mode mail retention days", Default: "0"},
		},
		Examples: []string{
			"inbaid serve",
			"inbaid serve --addr 0.0.0.0:8787",
			"INBAI_MAIL=imap INBAI_DOMAIN=example.com ... inbaid serve",
		},
		Run: serve,
	},
	{
		Name:     "version",
		Summary:  "Print the version",
		Usage:    []string{"inbaid version"},
		Examples: []string{"inbaid version"},
		NoFlags:  true,
		Run: func(_ []string, stdout, _ io.Writer, _ func(string) string) int {
			writef(stdout, "%s\n", version.String(version.Server))
			return 0
		},
	},
}
