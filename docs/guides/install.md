Status: partial — Compose can start PostgreSQL and Stalwart. `inbai init` is not implemented.

# Install

The supported server is one Linux host with Docker Engine, a public IPv4 address, and reverse DNS. macOS, Linux, and Windows run the CLI only.

The stack file is [deploy/compose](../../deploy/compose/README.md). It does not yet install inbAI itself. `inbai init` is still to be written.

## Planned sections

- Host requirements, including ports 25 and 443.
- `inbai init` and the files it writes.
- Where configuration and secrets live.
- How to upgrade a pinned Stalwart version.
- How to stop and remove the stack.
