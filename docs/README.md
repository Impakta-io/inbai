Status: current

# Documentation

The operator CLI calls `inbaid` for inbox and message commands. `init`, `domain add`, and `doctor` talk to Stalwart. Pages marked `Status: planned` name the work that will fill them in. Pages marked `Status: current` describe the repository as it is, or a rule that is already in force.

## Use cases

- [Use cases](use-cases.md)

## Design

- [Architecture](architecture.md)
- [Decision records](adr/README.md)

## Operators

Install, domain setup, the CLI, troubleshooting, and the quickstart describe the commands that exist. Backup and scaling are still outlines.

- [Quickstart](guides/quickstart.md)
- [Install](guides/install.md)
- [Domain setup](guides/domain-setup.md)
- [CLI](guides/cli.md)
- [Backup](guides/backup.md)
- [Troubleshooting](guides/troubleshooting.md)
- [Scaling](guides/scaling.md)

## Agents that use inbAI

The REST guide describes the routes `inbaid serve` implements. The other agent guides are outlines until MCP and the event feed exist.

- [Overview](agents/overview.md)
- [MCP](agents/mcp.md)
- [REST](agents/rest.md)
- [Events](agents/events.md)
- [Untrusted mail](agents/untrusted-mail.md)

## Contributors

- [Development](contributing/development.md)
- [Testing](contributing/testing.md)
- [Contributing with AI](contributing/ai-assisted.md)
- [Proposals](contributing/proposals.md)
- [Releases](contributing/releasing.md)
- [Mail backends](contributing/mail-backends.md)

## Reference

Generated from the CLI and from [api/openapi.yaml](../api/openapi.yaml) once those surfaces exist. See [reference](reference/README.md).
