Status: current

# Changelog

Versions follow semantic versioning. Pre-alpha releases are marked as such and callers should not depend on API stability yet. The release process is in [releasing.md](docs/contributing/releasing.md).

## v0.1.0-alpha.1 (2026-11-24)

Pre-alpha release.

- Operator CLI (`inbai`) and control-plane server (`inbaid`) published for macOS, Linux, and Windows, and via Homebrew (`brew install impakta-io/tap/inbai`).
- Stalwart JMAP integration: `init`, `domain add`, `domain relay`, `doctor`, inbox create/list/delete/generate, sender allowlists, per-inbox disk storage quotas, message send/list/search/get/reply/delete, and attachment upload/download.
- OAuth 2.1 client credentials, per-inbox grants (`read`, `send`, `reply`, `delete`, `manage`, `inbox:provision`), `inbai login`, audit log, retention sweeps, and send/rate quotas.
- Streamable HTTP MCP (`/mcp`) and stdio MCP (`inbai mcp serve`) with untrusted external-content markers.
- PostgreSQL-backed durable state, `FOR UPDATE SKIP LOCKED` job queue, event feed, signed webhooks (with private-address SSRF protection), Stalwart ingest hint, hard-bounce suppression, metrics, and `inbai backup`/`restore`.
- Generic IMAP/SMTP `MailBackend` (`INBAI_MAIL=imap`), framework examples, and `tools/load`.
