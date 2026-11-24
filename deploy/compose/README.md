Status: current

# Compose stack

This stack runs PostgreSQL 16, Stalwart, and `inbaid`. Stalwart's data store is PostgreSQL. That layout was exercised in the spike recorded in [ADR 0008](../../docs/adr/0008-stalwart-postgresql-store.md).

## Start

From this directory, with secrets that are not committed. Use a separate random value for each one:

```sh
export PG_PASSWORD="$(openssl rand -hex 24)"
export STALWART_RECOVERY_ADMIN_PASSWORD="$(openssl rand -hex 24)"
export INBAI_CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/inbai"
export STALWART_RECOVERY_MODE=1
mkdir -p "$INBAI_CONFIG_DIR"
docker compose up -d
```

Keep the two values somewhere safe. `docker compose up` stops with a message when either one is unset.

| Variable | What it protects | Why it is its own value |
| --- | --- | --- |
| `PG_PASSWORD` | The PostgreSQL superuser `stalwart`, which owns every mailbox record and the inbAI database. | Only `postgres`, `stalwart` and `inbaid` need it, on the Compose network. Port 5432 is not published. |
| `STALWART_RECOVERY_ADMIN_PASSWORD` | The Stalwart recovery administrator `admin`, which can change any account, domain and route. | It is the master key to the mail server, and the stack publishes ports 25, 587 and 443 on all interfaces. It should not also open the database. |
| `INBAI_RECOVERY_PASSWORD` | `inbai init` signs in with it and then sets the same value as the password of `operator@<domain>`, the login that can change the outbound route. | See the note after the next command. |

A leaked value then opens one door and not all three. The entrypoints accept any characters in `PG_PASSWORD` except a newline, carriage return or tab. They escape the value for the Stalwart JSON file and percent-encode it for the `inbaid` database URL. `openssl rand -hex 24` avoids the question.

`INBAI_OPERATOR_TOKEN` is optional. When it is unset, `inbai init` generates an operator token and stores it in `config.json`, which both `inbaid` and `inbai` read automatically. Set `INBAI_OPERATOR_TOKEN` only if you want to choose or override the token. It must be at least 32 characters, for example `openssl rand -hex 32`. `inbai init` and `inbaid` refuse a shorter one.

On macOS, set `INBAI_CONFIG_DIR` to `"$HOME/Library/Application Support/inbai"` instead. That is the directory `inbai init` writes unless `--config` selects another file. `INBAI_CONFIG_DIR` must be that directory, and the file inside it must be named `config.json`.

Recovery mode serves the management API on port 8080 inside the container. This file publishes that port on `127.0.0.1` only, so `inbai init` on the host can reach it. Do not publish port 8080 on a public address. `inbaid` publishes `127.0.0.1:8787` and waits until `config.json` exists.

```sh
export INBAI_RECOVERY_PASSWORD="$STALWART_RECOVERY_ADMIN_PASSWORD"
inbai init --url http://127.0.0.1:8080 --domain example.com
```

`inbai init` creates the domain, an operator login `operator@example.com`, and a service account. The operator password is `INBAI_RECOVERY_PASSWORD`. `inbai init` has one password input, so it must equal `STALWART_RECOVERY_ADMIN_PASSWORD` and the operator login starts with that value. It never equals `PG_PASSWORD`. Change the operator password in Stalwart afterwards if you want the recovery administrator and the operator to differ. The service API key is written to the config file and is not printed. Message retention defaults to 30 days.

The service account uses the service role from [ADR 0009](../../docs/adr/0009-impersonating-service-account.md). That role can impersonate inboxes. The operator account can also change the outbound route. The service account cannot.

The Postgres service also creates a database named `inbai` for the event log. The `inbaid` service receives `PG_PASSWORD` and its entrypoint builds the database URL `postgres://stalwart:<percent-encoded password>@postgres:5432/inbai?sslmode=disable` on the Compose network. This file does not publish port 5432. Inside the container, `INBAI_STALWART_URL` is `https://stalwart`, so the server does not use the `https://127.0.0.1` address stored for the host CLI. `INBAI_TLS_INSECURE=1` skips verification of the certificate Stalwart generates. The signing key is stored in that database, so another `inbaid` process that uses the same database and `--addr` verifies the same tokens. Set `INBAI_SIGNING_KEY` on every process only when you want to choose the key. That value must match the key already stored. `INBAI_SIGNING_KEY` must be at least 32 bytes, for example `openssl rand -hex 32`. Without a database, a missing key is generated in memory and never printed.

Leave recovery mode before creating an inbox. `inbaid` answers mail routes only after Stalwart is serving HTTPS.

```sh
export STALWART_RECOVERY_MODE=0
docker compose up -d
inbai domain add
```

Port 443 is Stalwart's HTTPS listener. The certificate is the one Stalwart generates, so `domain add` skips certificate verification when the stored URL is localhost. Copy the printed zone file into DNS. Run `inbai domain add` again to see which MX and TXT records are published.

Do not set `INBAI_OPERATOR_TOKEN` here. `inbai init` has already stored a generated token in `config.json`. The `inbaid` container reads that token because its own `INBAI_OPERATOR_TOKEN` is empty, and `inbai` on the host reads it from the same file. An exported value that differs from the stored token makes `inbai` send the wrong one and fail with 401. To choose the token yourself, export `INBAI_OPERATOR_TOKEN` before `inbai init` and before the first `docker compose up -d`.

```sh
inbai inbox create alice
inbai message send --from alice@example.com --to alice@example.com --subject hello --body hello
inbai message list alice@example.com
```

Do not run `inbaid serve` on the host next to the container. Port 5432 is not published, so a host process cannot reach this database. Tokens carry the listen address as their audience. The container listens on `0.0.0.0:8787` and a host process on `127.0.0.1:8787`, so one does not accept the other's tokens. To run a second replica that does share tokens and queues, give it the same database and the same `--addr`.

## Outbound relay

This file does not run a relay. After Stalwart has left recovery mode, `inbai domain relay` points other domains at an authenticated SMTP server. `INBAI_OPERATOR_PASSWORD` is the operator password from init. `INBAI_RELAY_PASSWORD` is the relay password. `inbai domain relay --off` restores MX delivery. The steps are in [domain setup](../../docs/guides/domain-setup.md).
