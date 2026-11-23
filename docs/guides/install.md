Status: partial — Compose starts PostgreSQL, Stalwart, and `inbaid`. `inbai init` still bootstraps Stalwart from the host. Host requirements, upgrades, and removing the stack are not a checked procedure yet.

# Install

The supported server is one Linux host with Docker Engine, a public IPv4 address, and reverse DNS. macOS, Linux, and Windows run the CLI only.

The stack file is [deploy/compose](../../deploy/compose/README.md). It starts PostgreSQL, Stalwart, and `inbaid`. `inbaid` waits until `inbai init` has written `config.json`, then listens on `127.0.0.1:8787`.

## First boot

Build the CLI, start the stack in recovery mode, and run init. The commands and the loopback-only recovery port are in the Compose document. Set `INBAI_OPERATOR_TOKEN` and `INBAI_CONFIG_DIR` before `docker compose up`. `INBAI_CONFIG_DIR` is the directory that contains `config.json`.

`inbai init` writes a config file. The default path is `$XDG_CONFIG_HOME/inbai/config.json`, or the platform user config directory when that variable is unset. On macOS that directory is `$HOME/Library/Application Support/inbai`. The file mode is `0600`. It contains the service API key, so do not commit it. `--config` chooses another path. Point `INBAI_CONFIG_DIR` at the directory of the file you use.

`INBAI_RECOVERY_PASSWORD` is the recovery administrator password. Init uses it once and stores an API key instead.

The container reaches Stalwart at `https://stalwart` and does not verify Stalwart's generated certificate. The config file keeps the host URL `https://127.0.0.1` for `inbai domain add` and `inbai doctor`.

## Planned sections

- Host requirements, including ports 25 and 443, written as a checked procedure.
- How to upgrade a pinned Stalwart version.
- How to stop and remove the stack.

## Planned sections

- Host requirements, including ports 25 and 443, written as a checked procedure.
- How to upgrade a pinned Stalwart version.
- How to stop and remove the stack.
