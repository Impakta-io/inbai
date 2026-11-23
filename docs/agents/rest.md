Status: partial — `inbaid serve` creates an inbox and can list, search, read, send, reply, and delete. OAuth and attachments are not implemented.

# REST API

The contract is [api/openapi.yaml](../../api/openapi.yaml). `inbaid serve` listens on `127.0.0.1:8787` unless `--addr` is set. It reads the config file written by `inbai init`.

## Operator token

Set `INBAI_OPERATOR_TOKEN` before starting the server. Send it as `Authorization: Bearer <token>`. A missing or rejected token is `401` with a problem document. Agent OAuth is not implemented, so this token is the only caller and it acts as the operator.

## Routes

`POST /v1/inboxes` with `{"localPart":"alice"}` creates `alice@<domain>` and returns the account id. `GET /v1/inboxes/{id}/messages` returns id, subject, and preview. Add `?q=` to search message text. `GET /v1/inboxes/{id}/messages/{messageId}` adds the plain-text body and threading headers. `POST /v1/inboxes/{id}/messages` submits `from`, `to`, `subject`, and `text`. `POST /v1/inboxes/{id}/messages/{messageId}/reply` does the same and sets `In-Reply-To` and `References` from the original message. `DELETE /v1/inboxes/{id}/messages/{messageId}` removes that message.

Create and send take an optional `Idempotency-Key` header. Repeating a completed key returns the first result and does not create or send again.

The server allows 50 submitted messages per inbox per UTC day and 500 per domain. The next send is `429`. A failed submit does not consume the quota. These counters live in the process and reset when `inbaid` restarts.

Errors use `application/problem+json` with `type`, `title`, `status`, and `detail`.

## Planned sections

- How to exchange a client credential for an access token.
- Cursor pagination.
- Attachments.
