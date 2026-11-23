Status: current

# inbAI

An inbox for every agent.

inbAI is open-source, self-hosted email infrastructure for AI agents. Operators use a command-line client. Agents use a REST API and an MCP server. There is no web inbox and no admin GUI.

This repository is pre-alpha. `inbai` prints its version, and `inbai init` bootstraps a Stalwart domain and service account. `inbaid` prints its version. Mail commands, the API, and the MCP server are not implemented yet.

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
