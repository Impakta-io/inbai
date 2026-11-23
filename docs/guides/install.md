Status: partial — Compose and `inbai init` can bootstrap Stalwart. The inbAI server is not installed by this stack.

# Install

The supported server is one Linux host with Docker Engine, a public IPv4 address, and reverse DNS. macOS, Linux, and Windows run the CLI only.

The stack file is [deploy/compose](../../deploy/compose/README.md). It starts PostgreSQL and Stalwart. It does not start `inbaid`.

## First boot

Build the CLI, start the stack in recovery mode, and run init. The commands and the loopback-only recovery port are in the Compose document.

`inbai init` writes a config file. The default path is `$XDG_CONFIG_HOME/inbai/config.json`, or the platform user config directory when that variable is unset. The file mode is `0600`. It contains the service API key, so do not commit it. `--config` chooses another path.

`INBAI_RECOVERY_PASSWORD` is the recovery administrator password. Init uses it once and stores an API key instead.

## Planned sections

- Host requirements, including ports 25 and 443, written as a checked procedure.
- How to upgrade a pinned Stalwart version.
- How to stop and remove the stack.
