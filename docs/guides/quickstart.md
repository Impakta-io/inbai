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
inbai init --domain inbai.test
```

This writes a local Compose stack for PostgreSQL and Stalwart, starts Stalwart in recovery mode, creates the domain, roles, accounts, service API key, operator password, and operator token in `config.json` (mode `0600`), and restarts Stalwart out of recovery mode. No environment variables are required. The first run pulls the PostgreSQL and Stalwart images and prints Docker's progress.

By default, local Docker mode binds loopback-only ports (`8443` for HTTPS, `2525` for SMTP, `5433` for PostgreSQL) so it will not clash with local services. Pass `--public` on a Linux server to bind ports `25` and `443` on all interfaces, or use [deploy/compose](../../deploy/compose/README.md) to run `inbaid` inside Compose as well. Port 25 receives inbound mail. Agents send through the inbAI API, so the stack publishes no SMTP submission port. Stalwart also listens on port 465 inside its container, but that port is not published.

`inbai.test` is a reserved name that never resolves, so it suits a local trial. `init` refuses to change the domain recorded in `config.json`. For a real domain, run `inbai init` with that domain on a fresh config instead. To print its DNS zone:

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
inbai message send --from alice@inbai.test --to alice@inbai.test --subject hello --body hello
inbai message list alice@inbai.test
```

`message list` prints the subject and a preview. Mail from the public internet arrives only after the DNS records are published and Stalwart accepts the delivery.

## Stop, remove, and rerun

To stop the local Docker stack later, run the `docker compose -f ... down` command printed by `inbai init`. Add `-v` to remove the volumes. That deletes the mail, the inboxes, and the Stalwart accounts.

`inbai init` is safe to run again with the same domain. It keeps the PostgreSQL password, the operator password, the operator token, and the retention period in `config.json`. It restarts Stalwart twice. After `down -v`, init checks the saved API key, sees that the new Stalwart refuses it, and saves a new one. Restart `inbaid` after a rerun that replaced the key.
