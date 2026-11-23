Status: current

# Compose stack

This stack is the start of the foundation. It runs PostgreSQL 16 and Stalwart 1.1.1, with Stalwart's data store in PostgreSQL. That layout was exercised in the spike recorded in [ADR 0008](../../docs/adr/0008-stalwart-postgresql-store.md).

`inbai init` does not exist yet. Until it does, the first boot is manual.

## Start

From this directory, with a password that is not committed:

```sh
export PG_PASSWORD='choose-a-password'
export STALWART_RECOVERY_MODE=1
export STALWART_RECOVERY_ADMIN="admin:${PG_PASSWORD}"
docker compose up -d
```

Recovery mode serves the management API on port 8080 inside the container and does not accept mail. Create the domain and the first administrator through that API, then start mail listeners:

```sh
export STALWART_RECOVERY_MODE=0
docker compose up -d
```

Port 443 is Stalwart's HTTPS listener. The certificate is the one Stalwart generates. Do not publish port 8080.

## What is not here yet

- `inbai init`, `inbai domain add`, and `inbai doctor`
- The inbAI server process
- An authenticated outbound relay

The relay path was proved in the spike and is not part of this file.
