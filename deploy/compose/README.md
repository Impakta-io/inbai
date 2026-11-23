Status: current

# Compose stack

This stack runs PostgreSQL 16, Stalwart, and `inbaid`. Stalwart's data store is PostgreSQL. That layout was exercised in the spike recorded in [ADR 0008](../../docs/adr/0008-stalwart-postgresql-store.md).

## Start

From this directory, with a password that is not committed:

```sh
export PG_PASSWORD='choose-a-password'
export INBAI_OPERATOR_TOKEN='choose-a-token'
export INBAI_CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/inbai"
export STALWART_RECOVERY_MODE=1
export STALWART_RECOVERY_ADMIN="admin:${PG_PASSWORD}"
mkdir -p "$INBAI_CONFIG_DIR"
docker compose up -d
```

On macOS, set `INBAI_CONFIG_DIR` to `"$HOME/Library/Application Support/inbai"` instead. That is the directory `inbai init` writes unless `--config` selects another file. `INBAI_CONFIG_DIR` must be that directory, and the file inside it must be named `config.json`.

Recovery mode serves the management API on port 8080 inside the container. This file publishes that port on `127.0.0.1` only, so `inbai init` on the host can reach it. Do not publish port 8080 on a public address. `inbaid` publishes `127.0.0.1:8787` and waits until `config.json` exists.

```sh
export INBAI_RECOVERY_PASSWORD="$PG_PASSWORD"
inbai init --url http://127.0.0.1:8080 --domain example.com
```

`inbai init` creates the domain, an operator login `operator@example.com`, and a service account. The operator password is `INBAI_RECOVERY_PASSWORD`. The service API key is written to the config file and is not printed. Message retention defaults to 30 days.

The operator account and the service account share the service role from [ADR 0009](../../docs/adr/0009-impersonating-service-account.md). That role can impersonate inboxes.

The Postgres service also creates a database named `inbai` for the event log. The `inbaid` service uses `postgres://stalwart:${PG_PASSWORD}@postgres:5432/inbai?sslmode=disable` on the Compose network. This file does not publish port 5432. Inside the container, `INBAI_STALWART_URL` is `https://stalwart`, so the server does not use the `https://127.0.0.1` address stored for the host CLI. `INBAI_TLS_INSECURE=1` skips verification of the certificate Stalwart generates. Set `INBAI_SIGNING_KEY` when more than one `inbaid` process must accept the same access tokens. Without it, the container prints a key once in its logs.

Leave recovery mode before creating an inbox. `inbaid` answers mail routes only after Stalwart is serving HTTPS.

```sh
export STALWART_RECOVERY_MODE=0
docker compose up -d
inbai domain add
```

Port 443 is Stalwart's HTTPS listener. The certificate is the one Stalwart generates, so `domain add` skips certificate verification when the stored URL is localhost. Copy the printed zone file into DNS. Run `inbai domain add` again to see which MX and TXT records are published.

```sh
export INBAI_OPERATOR_TOKEN='choose-a-token'
inbai inbox create alice
inbai message send --from alice@example.com --to alice@example.com --subject hello --body hello
inbai message list alice@example.com
```

Running `inbaid serve` on the host remains valid. Do not run that process and the Compose service against the same database at the same time unless `INBAI_SIGNING_KEY` is set on both.

## What is not here yet

- An authenticated outbound relay

The relay path was proved in the spike and is not part of this file.
