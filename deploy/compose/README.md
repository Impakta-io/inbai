Status: current

# Compose stack

This stack runs PostgreSQL 16 and Stalwart, with Stalwart's data store in PostgreSQL. That layout was exercised in the spike recorded in [ADR 0008](../../docs/adr/0008-stalwart-postgresql-store.md).

## Start

From this directory, with a password that is not committed:

```sh
export PG_PASSWORD='choose-a-password'
export STALWART_RECOVERY_MODE=1
export STALWART_RECOVERY_ADMIN="admin:${PG_PASSWORD}"
docker compose up -d
```

Recovery mode serves the management API on port 8080 inside the container. This file publishes that port on `127.0.0.1` only, so `inbai init` on the host can reach it. Do not publish port 8080 on a public address.

```sh
export INBAI_RECOVERY_PASSWORD="$PG_PASSWORD"
inbai init --url http://127.0.0.1:8080 --domain example.com
```

`inbai init` creates the domain, an operator login `operator@example.com`, and a service account. The operator password is `INBAI_RECOVERY_PASSWORD`. The service API key is written to the config file and is not printed. Message retention defaults to 30 days.

The operator account and the service account share the service role from [ADR 0009](../../docs/adr/0009-impersonating-service-account.md). That role can impersonate inboxes.

Leave recovery mode, then print the DNS records:

```sh
export STALWART_RECOVERY_MODE=0
docker compose up -d
inbai domain add
```

Port 443 is Stalwart's HTTPS listener. The certificate is the one Stalwart generates, so `domain add` skips certificate verification when the stored URL is localhost. Copy the printed zone file into DNS. Run `inbai domain add` again to see which MX and TXT records are published.

The Postgres service also creates a database named `inbai` for the event log. On the compose network its URL is `postgres://stalwart:${PG_PASSWORD}@postgres:5432/inbai?sslmode=disable`. This file does not publish port 5432. `inbaid serve` uses the log when `INBAI_DATABASE_URL` is set. Without that URL the event routes answer 503.

```sh
export INBAI_OPERATOR_TOKEN='choose-a-token'
inbaid serve
```

```sh
export INBAI_OPERATOR_TOKEN='choose-a-token'
inbai inbox create alice
inbai message send --from alice@example.com --to alice@example.com --subject hello --body hello
inbai message list alice@example.com
```

## What is not here yet

- The inbAI server process
- An authenticated outbound relay

The relay path was proved in the spike and is not part of this file.
