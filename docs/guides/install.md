Status: current — Compose starts PostgreSQL, Stalwart, and `inbaid`. `inbai init` bootstraps Stalwart from the host. Host requirements, upgrades, and teardown steps are below.

# Install

The supported server is one Linux host with Docker Engine, a public IPv4 address, and reverse DNS. macOS, Linux, and Windows run the CLI only.

The stack file is [deploy/compose](../../deploy/compose/README.md). It starts PostgreSQL, Stalwart, and `inbaid`. `inbaid` waits until `inbai init` has written `config.json`, then listens on `127.0.0.1:8787`.

## CLI binary

Install `inbai` and `inbaid` with Homebrew on macOS or Linux:

```sh
brew install impakta-io/tap/inbai
```

Prebuilt archives for macOS, Linux, and Windows are also published on [GitHub Releases](https://github.com/Impakta-io/inbai/releases). To build from source with Go 1.27.4 or newer, run `make build` (`bin/inbai` and `bin/inbaid`). See [releasing](../contributing/releasing.md) for release packaging details.

## First boot

For a standalone install (including Homebrew installs without cloning the repository), run:

```sh
inbai init --domain example.com
inbaid serve
```

`inbai init` writes the PostgreSQL and Stalwart Compose files into `<config-dir>/stack`, starts Stalwart in recovery mode, bootstraps the domain and service account, generates an operator token, writes `config.json`, and switches Stalwart out of recovery mode. Pass `--public` on a public mail host to bind ports `25` and `443` on all interfaces instead of loopback-only local ports. Port 25 receives inbound mail, and agents send through the inbAI API, so the stack publishes no SMTP submission port. Stalwart also listens on port 465 inside its container, but that port is not published.

If you prefer to manage Stalwart yourself or run `inbaid` inside Docker Compose as well, follow [deploy/compose](../../deploy/compose/README.md) and pass `--url` with `INBAI_RECOVERY_PASSWORD`.

`inbai init` writes a config file. The default path is `$XDG_CONFIG_HOME/inbai/config.json`, or the platform user config directory when that variable is unset. On macOS that directory is `$HOME/Library/Application Support/inbai`. The file mode is `0600`. A config directory that init creates gets mode `0700`. The file contains the service API key, operator token, and, in Docker mode, the operator password and database URL, so do not commit it. `--config` chooses another path.

When using an external Stalwart instance, `INBAI_RECOVERY_PASSWORD` is the recovery administrator password. Init uses it once, stores an API key, and sets the same value as the password for `operator@<domain>`. `inbai domain relay` asks for that password again as `INBAI_OPERATOR_PASSWORD`.

In the repository Compose file, the `inbaid` container reaches Stalwart at `https://stalwart` and does not verify Stalwart's generated certificate. The config file keeps the host URL (`https://127.0.0.1` or `https://127.0.0.1:8443`) for `inbai domain add`, `inbai doctor`, and host-run `inbaid serve`.

## Host requirements

- One Linux host with Docker Engine and the Compose plugin (`docker compose`).
- A public IPv4 address with reverse DNS (`PTR`) matching your mail hostname.
- Inbound TCP port `25` (SMTP) and port `443` (HTTPS) reachable from the internet. Outbound TCP port `25` is required for direct delivery, or outbound TCP port `587`/`465` when using an SMTP relay (`inbai domain relay`).
- Keep port `8080` (Stalwart recovery management) and port `8787` (`inbaid`) bound to `127.0.0.1` as in `deploy/compose/compose.yaml`, and place a TLS reverse proxy in front of `inbaid` if remote clients call it.

After DNS records are published, run `inbai doctor` to verify MX, TXT, PTR, port 25, TLS on port 443, and blocklists.

## Upgrade Stalwart

`deploy/compose/compose.yaml` pins the Stalwart image tag (`stalwartlabs/stalwart:v1`). Before upgrading:

1. Stop `inbaid` and back up both the inbAI database (`inbai backup`) and Stalwart's `stalwart` PostgreSQL database and `stalwart` volume. See [backup](backup.md).
2. Update the image tag in `deploy/compose/compose.yaml` (or pull a newer patch release on the same tag with `docker compose pull stalwart`).
3. Recreate Stalwart and start the stack:

```sh
docker compose up -d
```

4. Check `GET /healthz`, `GET /readyz`, and `inbai doctor`.

## Stop and remove the stack

To stop the containers while keeping the `postgres` and `stalwart` volumes:

```sh
docker compose down
```

To remove the containers and delete all stored mail, accounts, and inbAI state in those volumes:

```sh
docker compose down -v
```

Remove `$INBAI_CONFIG_DIR/config.json` as well if you are decommissioning the host.

The stack that `inbai init` starts in Docker mode takes the same commands with `-f <config-dir>/stack/compose.yaml`. After `down -v` there, `inbai init` can be run again with the same domain. It sees that the new Stalwart refuses the saved API key and saves a new one. With an external Stalwart, such as this Compose file, init does not check the key. Remove `config.json` before running init again after `down -v`.
