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

By default, local Docker mode binds loopback-only ports so it will not clash with local services. Pass `--public` on a Linux server to bind ports `25` and `443` on all interfaces, or use [deploy/compose](../../deploy/compose/README.md) to run `inbaid` inside Compose as well.

| Port | Default | With `--public` | Why |
| --- | --- | --- | --- |
| SMTP | `127.0.0.1:2525` | `25` on all interfaces | Stalwart receives inbound mail. |
| HTTPS | `127.0.0.1:8443` | `443` on all interfaces | Stalwart's HTTPS listener. `inbaid`, `domain add`, and `doctor` call its API here. |
| Recovery | `127.0.0.1:8080` | `127.0.0.1:8080` | Stalwart's management API in recovery mode. Only `inbai init` uses it. |
| PostgreSQL | `127.0.0.1:5433` | `127.0.0.1:5433` | The inbAI database and Stalwart's database. |
| `inbaid` | `127.0.0.1:8787` | `127.0.0.1:8787` | The REST API and MCP. `inbaid serve` runs on the host, not in the stack. |

Agents send through the inbAI API, so the stack publishes no SMTP submission port. Stalwart also listens on port 465 inside its container, but that port is not published.

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

The CLI uses the operator token that `init` stored as `operatorToken` in `<config-dir>/config.json`. The path for each OS is in [install](install.md#first-boot). To call the API with `curl`, export that value as `INBAI_OPERATOR_TOKEN`, or create an agent with `inbai agent create`, as in the [agent overview](../agents/overview.md). `inbai inbox list` prints each address and its account id, the `ACCOUNT_ID` the agent guides use.

## Stop, remove, and rerun

To stop the local Docker stack later, run the command printed by `inbai init`:

```sh
docker compose -f "<config-dir>/stack/compose.yaml" down
```

Stop `inbaid serve` with Ctrl-C first. Add `-v` to remove the volumes. That deletes the mail, the inboxes, the Stalwart accounts, and the inbAI database.

`inbai init` is safe to run again with the same domain. It keeps the PostgreSQL password, the operator password, the operator token, and the retention period in `config.json`. It restarts Stalwart twice. Run it again after upgrading `inbai`: the rerun adds any Stalwart permission or setting the new version needs. After `down -v`, init checks the saved API key, sees that the new Stalwart refuses it, and saves a new one. Restart `inbaid` after a rerun that replaced the key.
