Status: current

# inbAI

An inbox for every agent.

inbAI is open-source, self-hosted email infrastructure for AI agents. Operators use a command-line client (`inbai`). Agents use a REST API and an MCP server (`inbaid`). There is no web inbox and no admin GUI.

This repository is pre-alpha (`v0.1.0-alpha.1`). `inbai` bootstraps Stalwart (`init`), prints and checks DNS (`domain add`, `doctor`), configures an optional SMTP relay (`domain relay`), manages inboxes (create, list, delete, bulk generate, sender allowlists, disk storage quotas), sends and reads mail with attachments, manages agent credentials and grants, runs retention and backup/restore, and speaks stdio MCP (`inbai mcp serve`). `inbaid` serves the OpenAPI REST API, OAuth 2.1 client credentials, grant isolation, idempotency keys, send and rate quotas, Streamable HTTP MCP (`/mcp`), PostgreSQL-backed job queues, an event feed, signed webhooks, and hard-bounce suppression, plus an optional IMAP/SMTP backend (`INBAI_MAIL=imap`).

## Install

### Homebrew (macOS and Linux)

```sh
brew install impakta-io/tap/inbai
```

This installs both `inbai` and `inbaid`. Verify with `inbai version` and `inbaid version`.

### Prebuilt binaries

Download archives for macOS (`arm64`, `amd64`), Linux (`amd64`, `arm64`), or Windows (`amd64`), along with `checksums.txt`, from [GitHub Releases](https://github.com/Impakta-io/inbai/releases).

### Build from source

You need Go 1.27.4 or newer, and `make` on macOS or Linux.

```sh
make build test lint docs-check
```

The binaries are `bin/inbai` and `bin/inbaid`. Windows commands are in [development.md](docs/contributing/development.md).

### Run the server stack

`inbaid` runs alongside PostgreSQL and Stalwart using Docker Compose. Follow [deploy/compose/README.md](deploy/compose/README.md) or the [Quickstart](docs/guides/quickstart.md) to boot the stack, run `inbai init`, and send your first message:

```sh
cd deploy/compose
# Set PG_PASSWORD, INBAI_OPERATOR_TOKEN, INBAI_CONFIG_DIR, and STALWART_RECOVERY_MODE=1
docker compose up -d
```

See [Install](docs/guides/install.md) for host requirements, upgrades, and teardown.

## Read next

- [Quickstart](docs/guides/quickstart.md)
- [Install](docs/guides/install.md)
- [Use cases](docs/use-cases.md)
- [Documentation index](docs/README.md)
- [Architecture](docs/architecture.md)
- [Contributing](CONTRIBUTING.md), including [work done with AI](docs/contributing/ai-assisted.md)
- [Instructions for coding agents](AGENTS.md)

## Project

- License: [Apache-2.0](LICENSE)
- Conduct: [Contributor Covenant](CODE_OF_CONDUCT.md)
- Security: [how to report a vulnerability](SECURITY.md)
- Governance: [who maintains the project](GOVERNANCE.md)
