Status: current

# Changelog

Versions follow semantic versioning. Pre-alpha releases are marked as such and callers should not depend on API stability yet. The release process is in [releasing.md](docs/contributing/releasing.md).

## Unreleased

Changes merged since `v0.1.0-alpha.3`.

### Security

- A message read reports SPF, DKIM, and DMARC only from the top-most `Authentication-Results` header our own server wrote. Mail submitted on our own server carries no verified results (#76, #86).
- Mail is sent only as the inbox: its own address or one the mail server lets it send as. A `reply` grant answers only the original sender. Another `to` also needs `send` (#71).
- `inbaid` refuses short signing keys and operator tokens, and no longer prints a generated signing key (#73).
- A webhook stops when its creator loses `read` on the inbox or is suspended (#72).
- An agent token is refused with `503` when the server cannot read whether the agent is suspended (#68).
- Only the agent that started a bulk job, or an operator, can read it (#67).
- Unexpected errors return a request id instead of their text. The listener limits header, request, response, and idle time, and `POST /oauth/token` accepts at most 4 KiB (#66).
- IMAP mode verifies certificates, supports STARTTLS, and refuses a plaintext login to a host that is not loopback unless `INBAI_ALLOW_PLAINTEXT_AUTH=1` (#77).
- The CLI escapes ids it puts in API paths (#52).
- The Compose stack uses a separate secret for PostgreSQL and the Stalwart recovery administrator, and quotes them safely (#70).

### Changed

- **Upgrade:** Run `inbai init` again with the same domain. The rerun gives the inbox, service, and operator roles the delete permissions they lacked (#82), sets Stalwart to write its own `Authentication-Results` header on every message (#86), stores an operator password for `domain relay` in Docker mode (#69), and rewrites the Docker-mode stack without port 587 (#85). An external Stalwart must be in recovery mode for the rerun.
- **Upgrade:** `inbaid serve` exits with code 2 when `INBAI_SIGNING_KEY` is shorter than 32 bytes or the operator token is shorter than 32 characters. A short key already in the database also stops startup. Values that `inbai init` and `inbaid` generate pass. The fix is in [troubleshooting](docs/guides/troubleshooting.md#secret-too-short-on-startup) (#73).
- **Upgrade:** In `deploy/compose`, replace `STALWART_RECOVERY_ADMIN=admin:<password>` with `STALWART_RECOVERY_ADMIN_PASSWORD=<password>`. It and `PG_PASSWORD` are now required (#70).
- **Upgrade:** IMAP mode: `INBAI_IMAP_TLS=1` is deprecated and prints a notice. Set `INBAI_IMAP_SECURITY` and `INBAI_SMTP_SECURITY` instead. Without them, ports 143, 587, and 25 use STARTTLS. A deployment that sends on port 587 with `INBAI_IMAP_TLS=1` needs `INBAI_SMTP_SECURITY=starttls` (#77).
- The stacks no longer publish port 587. Agents send through the API. `inbai init --submission-port` is ignored (#85).
- Events are deleted 30 days after they are logged (`INBAI_EVENT_RETENTION_DAYS`, `0` keeps them). Finished webhook deliveries are deleted after 7 days (`INBAI_DELIVERY_RETENTION_DAYS`). Done and failed jobs and old send counters are pruned too (#83).
- Webhooks are delivered concurrently, one POST at a time per webhook (`INBAI_WEBHOOK_WORKERS`, default 16). A failed delivery is retried for about 31 hours. After a failure, events for one webhook can arrive out of `id` order (#83).
- IMAP mode keeps mail unless `INBAI_RETENTION_DAYS` or `inbai retention` turns retention on (#51).
- Message lists are newest first (#74).
- An `Idempotency-Key` is held while its request runs and bound to that request. A concurrent repeat is `409`, and the same key with a different request is `422` (#78).
- Creating an inbox whose address exists is `409`. The `operator` and `service` accounts are not listed, created, or deleted as inboxes (#82).
- `wait_for_message` takes an `after` cursor and returns one. Without `after` it waits for new changes only. It needs a database (#56).
- `inbai doctor` only reads. It reports a domain Stalwart does not have instead of creating it, and does not fail on an IPv6 address (#65).
- `inbai login --client-id` and `inbai login --clear` keep `operatorToken` (#62).
- `inbai help`, `inbai help <command>` and `--help` on every command print full help; `inbai` and `inbaid` with no arguments print help instead of the version (#97).

### Fixed

- Message delete, the retention sweep, and `inbai inbox delete` work on Stalwart after the `inbai init` rerun (#82).
- Deleting an inbox removes its grants, webhooks, allowlist, retention setting, event cursor, and create `Idempotency-Key` (#84).
- `docker compose down -v` removes every volume the stacks create. Stalwart's generated configuration lives in a temporary filesystem instead of a new anonymous volume per container (#87).
- A refused send or delete is `502` with the mail server's reason, and a refused send gives its quota back (#53).
- When the mail server can no longer report changes, the feed records a `resync` event and continues (#75).
- The event sweep continues past an inbox that fails (#54).
- `inbai init` is safe to rerun: it replaces an API key Stalwart refuses after `down -v`, keeps the retention period, writes `config.json` atomically, and prints the container logs on a timeout (#69).
- Large messages are read instead of failing on a truncated response (#64).
- `inbai mcp serve` renews its client-credential token (#55).
- IMAP mode selects the mailbox before searching, parses responses strictly, and names the account and step when a login fails (#63, #80).
- `inbai message reply` usage shows flags before arguments (#57).
- With several `inbaid` replicas, a webhook delivery is no longer claimed and posted twice when another replica finished it during the claim, and a late result from a sender whose lease ended no longer marks a delivered webhook as failed.

### Added

- `textTruncated` on a message whose body was cut at 1 MiB (#79).
- `GET /v1/webhooks` and `DELETE /v1/webhooks/{id}`. An agent may hold 10 webhooks per inbox (#72).
- MCP `list_messages` and `search_messages` take a `cursor` and return `next` (#74).
- `GET /v1/events?latest=true` (#56).
- `existing` in the bulk-generate result, which `inbai inbox generate` reports (#81, #82).
- `INBAI_AUTHSERV_ID` for IMAP mode (#76).
- `INBAI_IMAP_SECURITY`, `INBAI_SMTP_SECURITY`, `INBAI_ALLOW_PLAINTEXT_AUTH`, and `INBAI_IMAP_TLS_INSECURE` (#77).
- `inbai init --api-url` sets the `inbaid` address later commands use. A rerun keeps it (#87).

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
