Status: current

# inbAI

An inbox for every agent.

inbAI is open-source, self-hosted email infrastructure for AI agents. Operators use a command-line client. Agents use a REST API and an MCP server. There is no web inbox and no admin GUI.

This repository is pre-alpha. `inbai` prints its version. `inbai init` bootstraps a Stalwart domain and service account, `inbai domain add` prints the DNS records for that domain, and `inbai doctor` checks the host. `inbai inbox create`, `inbox generate`, and the `message` commands call `inbaid`. `inbaid serve` exposes create, bulk generate, list, search, read, send, reply, and delete over HTTP, including attachments, an operator bearer token, an idempotency key, and a daily send quota. Agents authenticate with OAuth client credentials. `inbaid` serves the same mail operations over MCP at `/mcp`, and `inbai mcp serve` serves them on stdio. With `INBAI_DATABASE_URL` set, it also serves an event feed, signed webhooks, and a Stalwart ingest hint.

## Build

You need Go 1.27.4 or newer, and `make` on macOS or Linux.

```sh
make build test lint docs-check
```

The binaries are `bin/inbai` and `bin/inbaid`. Windows commands are in [development.md](docs/contributing/development.md).

## Read next

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
