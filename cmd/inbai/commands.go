// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"os"

	"github.com/Impakta-io/inbai/cmd/internal/cli"
	"github.com/Impakta-io/inbai/internal/version"
)

// program is the inbai command table. Dispatch, help, and
// docs/reference/cli.md all read it. A new command needs a row here.
var program = &cli.Program{
	Name:    "inbai",
	Summary: "Operator CLI for inbAI, self-hosted email for AI agents.",
	Sections: []cli.Section{
		{Title: "Setup", Names: []string{"init", "doctor", "domain add", "domain relay"}},
		{Title: "Inboxes", Names: []string{"inbox create", "inbox list", "inbox delete", "inbox generate", "inbox allowlist", "inbox quota"}},
		{Title: "Mail", Names: []string{"message send", "message list", "message get", "message reply", "message delete", "message attachment"}},
		{Title: "Agents and access", Names: []string{"agent create", "agent grant", "agent revoke", "agent suspend", "agent resume", "login"}},
		{Title: "Server data", Names: []string{"retention", "backup", "restore", "audit"}},
		{Title: "Integration", Names: []string{"mcp serve"}},
		{Title: "Other", Names: []string{"version", "help"}},
	},
	Footer: `Configuration:
  inbai/config.json in the user config directory: $XDG_CONFIG_HOME or ~/.config
  on Linux, ~/Library/Application Support on macOS, %AppData% on Windows.
  --config names another file. Credentials, first set wins:
  INBAI_OPERATOR_TOKEN; INBAI_CLIENT_ID with INBAI_CLIENT_SECRET; client
  credentials from inbai login; operatorToken in the config file.
`,
	DocsURL:  "https://github.com/Impakta-io/inbai/tree/main/docs",
	Commands: commands,
}

var commands = []cli.Command{
	{
		Name:    "init",
		Summary: "Set up Stalwart, PostgreSQL, the domain, and config.json",
		Usage:   []string{"inbai init --domain name [--public] [--no-docker] [--url url] [--api-url url] [--hostname name] [--stalwart-url url] [--stack-dir dir] [--config path] [--json]"},
		Description: "With Docker Compose, init writes a stack into <config-dir>/stack, starts PostgreSQL and Stalwart, " +
			"bootstraps the domain, and writes config.json with mode 0600. Ports bind to loopback unless --public is given.\n\n" +
			"With --url or --no-docker, init bootstraps a Stalwart that is already running, using INBAI_RECOVERY_PASSWORD. " +
			"Running init again keeps the domain, accounts, passwords, tokens, and retention already saved.",
		Env: []cli.EnvVar{
			{Name: "INBAI_RECOVERY_PASSWORD", Text: "Stalwart recovery password; selects external mode"},
			{Name: "INBAI_OPERATOR_TOKEN", Text: "operator token, when the config has none yet"},
			{Name: "COMPOSE_PROJECT_NAME", Text: "Docker Compose project name"},
		},
		Examples: []string{
			"inbai init --domain example.com",
			"inbai init --domain example.com --public",
			"INBAI_RECOVERY_PASSWORD=... inbai init --url http://127.0.0.1:8080 --domain example.com",
		},
		Run: initCmd,
	},
	{
		Name:    "doctor",
		Summary: "Check DNS, reverse DNS, port 25, TLS, and blocklists",
		Usage:   []string{"inbai doctor [--domain name] [--config path] [--tls-insecure] [--json]"},
		Description: "doctor checks the MX and TXT records Stalwart publishes, reverse DNS, port 25, TLS on port 443, " +
			"and the Spamhaus ZEN and SpamCop blocklists. It exits 0 when every check passes and 1 when one needs a fix.",
		Examples: []string{"inbai doctor", "inbai doctor --json"},
		Run: func(args []string, stdout, stderr io.Writer, _ func(string) string) int {
			return doctorCmd(args, stdout, stderr)
		},
	},
	{
		Name:    "domain",
		Summary: "Publish the domain and route outbound mail",
	},
	{
		Name:        "domain add",
		Summary:     "Print the domain's DNS records and check them",
		Usage:       []string{"inbai domain add [--domain name] [--config path] [--tls-insecure] [--json]"},
		Description: "domain add creates the domain in Stalwart when it is missing, prints its DNS zone file, and checks the MX and TXT records.",
		Examples:    []string{"inbai domain add", "inbai domain add --json"},
		Run: func(args []string, stdout, stderr io.Writer, _ func(string) string) int {
			return domainAdd(append([]string{"add"}, args...), stdout, stderr)
		},
	},
	{
		Name:    "domain relay",
		Summary: "Send mail for other domains through an SMTP relay",
		Usage: []string{
			"inbai domain relay --host name --user name [--port n] [--config path] [--json]",
			"inbai domain relay --off [--config path] [--json]",
		},
		Description: "Mail for a domain this server hosts stays local. --off sends other domains to their MX hosts again. " +
			"The command signs in to Stalwart as operator@<domain>.",
		Env: []cli.EnvVar{
			{Name: "INBAI_RELAY_PASSWORD", Text: "SMTP relay password"},
			{Name: "INBAI_OPERATOR_PASSWORD", Text: "operator password (default the one init stored)"},
			{Name: "INBAI_RECOVERY_PASSWORD", Text: "used when neither of the above is set"},
		},
		Examples: []string{
			"INBAI_RELAY_PASSWORD=... inbai domain relay --host smtp.example.net --user mailer",
			"inbai domain relay --off",
		},
		Run: func(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
			return domainRelay(append([]string{"relay"}, args...), stdout, stderr, getenv)
		},
	},
	{
		Name:    "inbox",
		Summary: "Create, list, and configure inboxes",
	},
	{
		Name:    "inbox create",
		Summary: "Create one inbox address",
		Usage:   []string{"inbai inbox create [--quota-bytes n] <local-part>"},
		Description: "The address is <local-part>@<domain> for the config domain. The address and its account id are " +
			"written into the config file, which the message commands read.",
		Examples:   []string{"inbai inbox create alice", "inbai inbox create --quota-bytes 52428800 bob"},
		Positional: true,
		Run:        inboxCreate,
	},
	{
		Name:        "inbox list",
		Summary:     "List inboxes and their account ids",
		Usage:       []string{"inbai inbox list [--limit n] [--cursor token] [--config path] [--json]"},
		Description: "Each line is an address and its account id, the <inbox-id> other commands take. A line that starts with next is the cursor for the next page.",
		Examples:    []string{"inbai inbox list", "inbai inbox list --limit 50 --cursor <token>"},
		Run:         inboxList,
	},
	{
		Name:        "inbox delete",
		Summary:     "Delete an inbox",
		Usage:       []string{"inbai inbox delete <address>"},
		Description: "The address must be in the config file. The command deletes that account and removes the address from the config file.",
		Examples:    []string{"inbai inbox delete alice@example.com"},
		Positional:  true,
		Run:         inboxDelete,
	},
	{
		Name:        "inbox generate",
		Summary:     "Create many numbered inboxes",
		Usage:       []string{"inbai inbox generate --count <n> [--prefix <text>]"},
		Description: "Addresses are <prefix>00000@<domain> and up. A large request runs as a job, and the command prints its progress. Addresses that already exist are skipped.",
		Examples:    []string{"inbai inbox generate --count 10", "inbai inbox generate --count 100 --prefix test"},
		Run:         inboxGenerate,
	},
	{
		Name:        "inbox allowlist",
		Summary:     "Get, set, or clear an inbox's sender allowlist",
		Description: "An entry is one address or one domain, and a list holds at most 100. The list does not stop mail from arriving: a message read reports whether the sender matched.",
	},
	{
		Name:       "inbox allowlist get",
		Summary:    "Print an inbox's sender allowlist",
		Usage:      []string{"inbai inbox allowlist get [--config path] [--json] <inbox-id>"},
		Examples:   []string{"inbai inbox allowlist get <inbox-id>"},
		Positional: true,
		Run:        inboxAllowlistGet,
	},
	{
		Name:       "inbox allowlist set",
		Summary:    "Replace an inbox's sender allowlist",
		Usage:      []string{"inbai inbox allowlist set [--config path] [--json] <inbox-id> <sender> [<sender>...]"},
		Examples:   []string{"inbai inbox allowlist set <inbox-id> ada@example.com example.org"},
		Positional: true,
		Run:        inboxAllowlistSet,
	},
	{
		Name:       "inbox allowlist clear",
		Summary:    "Remove an inbox's sender allowlist",
		Usage:      []string{"inbai inbox allowlist clear [--config path] [--json] <inbox-id>"},
		Examples:   []string{"inbai inbox allowlist clear <inbox-id>"},
		Positional: true,
		Run:        inboxAllowlistClear,
	},
	{
		Name:        "inbox quota",
		Summary:     "Get or set an inbox's storage quota",
		Description: "<inbox-id> may also be an address in the config file. New inboxes get the server default, 100 MiB unless inbaid sets INBAI_STORAGE_QUOTA_BYTES.",
	},
	{
		Name:       "inbox quota get",
		Summary:    "Print an inbox's storage quota and usage",
		Usage:      []string{"inbai inbox quota get [--config path] [--json] <inbox-id>"},
		Examples:   []string{"inbai inbox quota get alice@example.com"},
		Positional: true,
		Run:        inboxQuotaGet,
	},
	{
		Name:       "inbox quota set",
		Summary:    "Set an inbox's storage quota in bytes, or 0 for none",
		Usage:      []string{"inbai inbox quota set [--config path] [--json] <inbox-id> <limit-bytes>"},
		Examples:   []string{"inbai inbox quota set alice@example.com 52428800"},
		Positional: true,
		Run:        inboxQuotaSet,
	},
	{
		Name:        "message",
		Summary:     "Send and read mail as an inbox in the config file",
		Description: "An <address> must be in the inboxes map of the config file, which inbox create and inbox generate fill.",
	},
	{
		Name:     "message send",
		Summary:  "Send a plain-text message",
		Usage:    []string{"inbai message send --from <address> --to <address> --subject <text> [--body <text>] [--attach <file>]"},
		Examples: []string{"inbai message send --from alice@example.com --to bob@example.net --subject hello --body 'Hi Bob'"},
		Run:      messageSend,
	},
	{
		Name:        "message list",
		Summary:     "List or search an inbox's messages",
		Usage:       []string{"inbai message list [--query text] <address>"},
		Description: "Each line is a subject and a preview. A line that starts with next is the cursor for the next page.",
		Examples:    []string{"inbai message list alice@example.com", "inbai message list --query invoice --limit 20 alice@example.com"},
		Positional:  true,
		Run:         messageList,
	},
	{
		Name:        "message get",
		Summary:     "Print one message and its attachments",
		Usage:       []string{"inbai message get <address> <message-id>"},
		Description: "Each attachment line is a blob id and a file name. Pass the blob id to message attachment.",
		Examples:    []string{"inbai message get alice@example.com <message-id>", "inbai message get --json alice@example.com <message-id>"},
		Positional:  true,
		Run:         messageGet,
	},
	{
		Name:       "message reply",
		Summary:    "Reply to a message",
		Usage:      []string{"inbai message reply --to <address> [--body <text>] <address> <message-id>"},
		Examples:   []string{"inbai message reply --to bob@example.net --body 'Thanks' alice@example.com <message-id>"},
		Positional: true,
		Run:        messageReply,
	},
	{
		Name:       "message delete",
		Summary:    "Delete a message",
		Usage:      []string{"inbai message delete <address> <message-id>"},
		Examples:   []string{"inbai message delete alice@example.com <message-id>"},
		Positional: true,
		Run:        messageDelete,
	},
	{
		Name:       "message attachment",
		Summary:    "Save an attachment to a file",
		Usage:      []string{"inbai message attachment --output <file> <address> <message-id> <blob-id>"},
		Examples:   []string{"inbai message attachment --output note.txt alice@example.com <message-id> <blob-id>"},
		Positional: true,
		Run:        messageAttachment,
	},
	{
		Name:    "agent",
		Summary: "Create agents and manage their permissions",
		Description: "The permissions on an inbox are read, send, reply, delete, and manage. " +
			"inbox:provision on inbox * lets an agent create inboxes.",
	},
	{
		Name:       "agent create",
		Summary:    "Create an agent and print its secret once",
		Usage:      []string{"inbai agent create <name>"},
		Examples:   []string{"inbai agent create ada", "inbai agent create --json ada"},
		Positional: true,
		Run:        agentCreate,
	},
	{
		Name:        "agent grant",
		Summary:     "Grant an agent one permission on an inbox",
		Usage:       []string{"inbai agent grant <agent> <inbox> <permission>"},
		Description: "<permission> is read, send, reply, delete, or manage on an inbox id, or inbox:provision on '*'.",
		Examples:    []string{"inbai agent grant ada <inbox-id> read", "inbai agent grant ada '*' inbox:provision"},
		Positional:  true,
		Run:         agentGrant,
	},
	{
		Name:       "agent revoke",
		Summary:    "Revoke one permission from an agent",
		Usage:      []string{"inbai agent revoke <agent> <inbox> <permission>"},
		Examples:   []string{"inbai agent revoke ada <inbox-id> read"},
		Positional: true,
		Run:        agentRevoke,
	},
	{
		Name:       "agent suspend",
		Summary:    "Stop an agent from getting or using tokens",
		Usage:      []string{"inbai agent suspend <name>"},
		Examples:   []string{"inbai agent suspend ada"},
		Positional: true,
		Run:        agentSuspend,
	},
	{
		Name:       "agent resume",
		Summary:    "Let a suspended agent get and use tokens again",
		Usage:      []string{"inbai agent resume <name>"},
		Examples:   []string{"inbai agent resume ada"},
		Positional: true,
		Run:        agentResume,
	},
	{
		Name:    "login",
		Summary: "Check credentials and save them in the config file",
		Usage:   []string{"inbai login [--url url] [--client-id id] [--clear] [--config path] [--json]"},
		Description: "With --client-id (or INBAI_CLIENT_ID) and INBAI_CLIENT_SECRET, login checks and stores the client " +
			"credentials, which later commands use before operatorToken. Otherwise it checks and stores INBAI_OPERATOR_TOKEN " +
			"and removes stored client credentials. --clear removes the stored client credentials only.",
		Env: []cli.EnvVar{
			{Name: "INBAI_OPERATOR_TOKEN", Text: "operator token to store"},
			{Name: "INBAI_CLIENT_ID", Text: "OAuth client id, instead of --client-id"},
			{Name: "INBAI_CLIENT_SECRET", Text: "OAuth client secret"},
		},
		Examples: []string{
			"INBAI_OPERATOR_TOKEN=... inbai login --url https://inbai.example.com",
			"INBAI_CLIENT_SECRET=... inbai login --client-id <client-id>",
			"inbai login --clear",
		},
		Run: loginCmd,
	},
	{
		Name:        "retention",
		Summary:     "Print or set how many days mail is kept",
		Usage:       []string{"inbai retention [--days n] [--inbox id] [--config path] [--json]"},
		Description: "--days 0 keeps mail. With --inbox, only that inbox changes. inbaid deletes older mail about once an hour.",
		Examples:    []string{"inbai retention", "inbai retention --days 14", "inbai retention --inbox <inbox-id> --days 7"},
		Run:         retentionCmd,
	},
	{
		Name:     "retention run",
		Summary:  "Delete mail older than the retention period now",
		Usage:    []string{"inbai retention run [--config path] [--json]"},
		Examples: []string{"inbai retention run"},
		Run:      retentionRun,
	},
	{
		Name:        "backup",
		Summary:     "Write the inbAI database to a file",
		Usage:       []string{"inbai backup --file path [--json]"},
		Description: "The file has no message bodies. Back up the Stalwart database and volume as well. backup does not read the config file.",
		Env:         []cli.EnvVar{{Name: "INBAI_DATABASE_URL", Text: "inbAI PostgreSQL URL (required)"}},
		Examples:    []string{"INBAI_DATABASE_URL=postgres://... inbai backup --file inbai.backup"},
		Run:         backupCmd,
	},
	{
		Name:        "restore",
		Summary:     "Replace the inbAI database from a file",
		Usage:       []string{"inbai restore --file path [--json]"},
		Description: "restore does not read the config file.",
		Env:         []cli.EnvVar{{Name: "INBAI_DATABASE_URL", Text: "inbAI PostgreSQL URL (required)"}},
		Examples:    []string{"INBAI_DATABASE_URL=postgres://... inbai restore --file inbai.backup"},
		Run:         restoreCmd,
	},
	{
		Name:        "audit",
		Summary:     "List agent, grant, and inbox changes",
		Usage:       []string{"inbai audit [--after n] [--limit n] [--config path] [--json]"},
		Description: "Each line is an id, an action, the actor, and the target. A line that starts with next is the id to pass to --after.",
		Examples:    []string{"inbai audit", "inbai audit --after 120 --limit 50"},
		Run:         auditCmd,
	},
	{
		Name:    "mcp",
		Summary: "Serve the Model Context Protocol",
	},
	{
		Name:    "mcp serve",
		Summary: "Speak MCP on stdin and stdout",
		Usage:   []string{"inbai mcp serve [--config path]"},
		Description: "The first credential that is set wins: INBAI_ACCESS_TOKEN, INBAI_CLIENT_ID with INBAI_CLIENT_SECRET, " +
			"INBAI_OPERATOR_TOKEN, client credentials in the config file, operatorToken in the config file. " +
			"A token from client credentials is renewed before it expires.",
		Env: []cli.EnvVar{
			{Name: "INBAI_ACCESS_TOKEN", Text: "access token to use as is"},
			{Name: "INBAI_CLIENT_ID", Text: "OAuth client id"},
			{Name: "INBAI_CLIENT_SECRET", Text: "OAuth client secret"},
			{Name: "INBAI_OPERATOR_TOKEN", Text: "operator token"},
		},
		Examples: []string{"inbai mcp serve", "inbai mcp serve --config /etc/inbai/agent.json"},
		Run: func(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
			return mcpCmd(append([]string{"serve"}, args...), os.Stdin, stdout, stderr, getenv)
		},
	},
	{
		Name:     "version",
		Summary:  "Print the version",
		Usage:    []string{"inbai version"},
		Examples: []string{"inbai version"},
		NoFlags:  true,
		Run: func(_ []string, stdout, _ io.Writer, _ func(string) string) int {
			writef(stdout, "%s\n", version.String(version.CLI))
			return 0
		},
	},
}
