Status: current

# Changelog

Versions follow semantic versioning. Pre-alpha releases are marked as such and callers should not depend on API stability yet. The release process is in [releasing.md](docs/contributing/releasing.md).

## v0.1.0-alpha.3 (2026-11-24)

Pre-alpha release.

- Mail sent through the Stalwart backend is filed in the sender's `Sent` mailbox, marked read, instead of as a draft in `Inbox`. It falls back to `Drafts`, then `Inbox`, only if `Sent` does not exist. Existing installs do not need to run `inbai init` again.
- `message list` and `message search` return only received mail from `Inbox`. Sending to yourself no longer shows two copies.
- Sending to another recipient no longer adds an outbound message to the sender's event feed.

## v0.1.0-alpha.2 (2026-11-24)

Pre-alpha release.

- `inbai init --domain <name>` starts a local PostgreSQL and Stalwart stack in Docker, bootstraps it, and writes the config. It needs no environment variables and no repository clone.
- Local Docker mode binds loopback-only ports (`8443`, `2525`, `5870`, `5433`, `8080`). `--public` binds `25`, `587`, and `443` on all interfaces.
- `inbai init` generates an operator token and stores it, with the database URL, in `config.json` (mode `0600`). `inbaid serve` and `inbai` read both from there. `INBAI_OPERATOR_TOKEN` and `INBAI_DATABASE_URL` still override them.
- The README has an install section, and the install, quickstart, and agent guides match the published release.

## v0.1.0-alpha.1 (2026-11-24)

Pre-alpha release.

- Operator CLI (`inbai`) and control-plane server (`inbaid`) published for macOS, Linux, and Windows, and via Homebrew (`brew install impakta-io/tap/inbai`).
- Stalwart JMAP integration: `init`, `domain add`, `domain relay`, `doctor`, inbox create/list/delete/generate, sender allowlists, per-inbox disk storage quotas, message send/list/search/get/reply/delete, and attachment upload/download.
- OAuth 2.1 client credentials, per-inbox grants (`read`, `send`, `reply`, `delete`, `manage`, `inbox:provision`), `inbai login`, audit log, retention sweeps, and send/rate quotas.
- Streamable HTTP MCP (`/mcp`) and stdio MCP (`inbai mcp serve`) with untrusted external-content markers.
- PostgreSQL-backed durable state, `FOR UPDATE SKIP LOCKED` job queue, event feed, signed webhooks (with private-address SSRF protection), Stalwart ingest hint, hard-bounce suppression, metrics, and `inbai backup`/`restore`.
- Generic IMAP/SMTP `MailBackend` (`INBAI_MAIL=imap`), framework examples, and `tools/load`.
