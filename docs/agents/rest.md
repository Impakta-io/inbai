Status: partial — `inbaid serve` creates, lists, and deletes inboxes, and can list, search, read, send, reply, and delete messages, including attachment upload and download. Operators and agents authenticate as described below. MCP is a separate transport, described in [MCP](mcp.md).

# REST API

The contract is [api/openapi.yaml](../../api/openapi.yaml). `inbaid serve` listens on `127.0.0.1:8787` unless `--addr` is set. It reads the config file written by `inbai init`.

## Operator token and agent tokens

Set `INBAI_OPERATOR_TOKEN` before starting the server. That bearer token acts as the operator. An agent exchanges a client id and secret at `POST /oauth/token` with `grant_type=client_credentials` and then sends the access token as `Authorization: Bearer`. The token lives ten minutes and only names the agent. Grants decide what the agent may do. A missing or rejected token is `401` with a problem document and a `WWW-Authenticate` header that points at the protected-resource metadata.

## Routes

`POST /v1/inboxes` with `{"localPart":"alice"}` creates `alice@<domain>` and returns the account id. `GET /v1/inboxes` lists accounts for an operator. `limit` defaults to 50 and cannot be more than 100. The response includes `next` when another page remains. Pass that opaque value as `cursor`. `DELETE /v1/inboxes/{id}` removes one account and is also operator-only. `POST /v1/inboxes/generate` with `{"prefix":"u","count":10}` creates `u00000` through `u00009`. A count from 51 to 10000 returns `202` and a job id. `GET /v1/jobs/{id}` reports `running`, `done`, or `failed`. `GET /v1/inboxes/{id}/messages` returns id, subject, and preview. Add `?q=` to search message text. `limit` and `cursor` page the list. `next` is omitted on the last page. `GET /v1/inboxes/{id}/messages/{messageId}` adds the plain-text body, threading headers, and attachment names with `blobId`. `GET /v1/inboxes/{id}/messages/{messageId}/attachments/{blobId}` returns the file bytes. `POST /v1/inboxes/{id}/messages` submits `from`, `to`, `subject`, `text`, and optional base64 `attachments`. `POST /v1/inboxes/{id}/messages/{messageId}/reply` does the same and sets `In-Reply-To` and `References` from the original message. `DELETE /v1/inboxes/{id}/messages/{messageId}` removes that message.

Create and send take an optional `Idempotency-Key` header. Repeating a completed key returns the first result and does not create or send again.

The server allows 50 submitted messages per inbox per UTC day and 500 per domain. The next send is `429`. A failed submit does not consume the quota. These counters live in the process and reset when `inbaid` restarts.

Errors use `application/problem+json` with `type`, `title`, `status`, and `detail`.

## Planned sections

- How to exchange a client credential for an access token.
