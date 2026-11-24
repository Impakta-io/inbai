Status: partial — install, domain setup, inbox creation, and a local send are documented. Public delivery still depends on DNS and a healthy `doctor` report.

# Quickstart

This walks an operator from a fresh install to one inbox. An agent connects with a client credential, described in the [agent overview](../agents/overview.md). MCP is described in [MCP](../agents/mcp.md).

## Install

Install `inbai` and `inbaid` with Homebrew (or download a release archive / build from source — see [install](install.md)):

```sh
brew install impakta-io/tap/inbai
```

You need Docker with the Compose plugin (`docker compose`) running locally.

## Initialize

Run `inbai init` with your domain:

```sh
inbai init --domain example.com
```

This writes a local Compose stack for PostgreSQL and Stalwart, starts Stalwart in recovery mode, creates the domain, roles, accounts, service API key, and operator token in `config.json` (mode `0600`), and restarts Stalwart out of recovery mode. No environment variables are required.

By default, local Docker mode binds loopback-only ports (`8443` for HTTPS, `2525` for SMTP, `5870` for submission, `5433` for PostgreSQL) so it will not clash with local services. Pass `--public` on a Linux server to bind ports `25`, `587`, and `443` on all interfaces, or use [deploy/compose](../../deploy/compose/README.md) to run `inbaid` inside Compose as well.

Print the DNS zone if you are setting up a real domain:

```sh
inbai domain add
```

Publish the zone, then run `inbai doctor`. A healthy host exits 0. The local certificate is self-signed, so the TLS line stays a fix until a real certificate is installed.

## Start the server and create an inbox

Start `inbaid` (it reads the operator token, Stalwart URL, and PostgreSQL URL from `config.json` automatically):

```sh
inbaid serve
```

In a second terminal:

```sh
inbai inbox create alice
inbai message send --from alice@example.com --to alice@example.com --subject hello --body hello
inbai message list alice@example.com
```

`message list` prints the subject and a preview. Mail from the public internet arrives only after the DNS records are published and Stalwart accepts the delivery.

To stop the local Docker stack later, run the `docker compose -f ... down` command printed by `inbai init` (add `-v` to remove the volumes).
