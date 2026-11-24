Status: current — `inbaid serve` creates, lists, and deletes inboxes, manages sender allowlists and retention, and can list, search, read, send, reply, and delete messages, including attachment upload and download. Operators and agents authenticate as described below. MCP is a separate transport, described in [MCP](mcp.md).

# REST API

The contract is [api/openapi.yaml](../../api/openapi.yaml). `inbaid serve` listens on `127.0.0.1:8787` unless `--addr` is set. It reads the config file written by `inbai init`.

## Operator token and agent tokens

Set `INBAI_OPERATOR_TOKEN` before starting the server. That bearer token acts as the operator. An agent exchanges a client id and secret at `POST /oauth/token` with `grant_type=client_credentials` and then sends the access token as `Authorization: Bearer`. The token lives ten minutes and only names the agent. Grants decide what the agent may do. A missing or rejected token is `401` with a problem document and a `WWW-Authenticate` header that points at the protected-resource metadata. If the server cannot read whether the agent is suspended, the token is not accepted and the answer is `503`. Retry the same token later.

## Routes

`POST /v1/inboxes` with `{"localPart":"alice"}` creates `alice@<domain>` and returns the account id. Optional `quotaBytes` overrides the server's default per-inbox disk storage cap (100 MiB, configurable with `INBAI_STORAGE_QUOTA_BYTES`). `GET /v1/inboxes/{id}/quota` reads the storage quota and current usage in bytes (`limitBytes`, `usedBytes`), and `PUT /v1/inboxes/{id}/quota` with `{"limitBytes":...}` sets or clears it (`0` clears the cap; backends without storage quota support return `501`). `GET /v1/inboxes` lists accounts for an operator. `limit` defaults to 50 and cannot be more than 100. The response includes `next` when another page remains. Pass that opaque value as `cursor`. `DELETE /v1/inboxes/{id}` removes one account and is also operator-only. `POST /v1/inboxes/generate` with `{"prefix":"u","count":10}` creates `u00000` through `u00009`. A count from 51 to 10000 returns `202` and a job id. `GET /v1/jobs/{id}` reports `running`, `done`, or `failed`. An operator can read any job. An agent can read a job it started while it holds `inbox:provision`. Any other caller gets `404`, the same as for an unknown id. `GET /v1/inboxes/{id}/messages` returns id, subject, and preview. Add `?q=` to search message text. `limit` and `cursor` page the list. `next` is omitted on the last page. `GET /v1/inboxes/{id}/messages/{messageId}` adds the plain-text body, threading headers, and attachment names with `blobId`. `GET /v1/inboxes/{id}/messages/{messageId}/attachments/{blobId}` returns the file bytes. `POST /v1/inboxes/{id}/messages` submits `to`, `subject`, `text`, optional `from`, and optional base64 `attachments`. Without `from` the message is sent as the inbox's own address. Any other `from` must be an address the mail server lets that inbox send as, compared without case, or the call is `403`. That applies to the operator too. `POST /v1/inboxes/{id}/messages/{messageId}/reply` takes the same fields and sets `In-Reply-To` and `References` from the original message. The reply goes to the original message's first `Reply-To` address, or to its `From` when it has no `Reply-To`. `to` may be omitted or set to that address. A different `to` also needs `send` on the inbox, or the call is `403`. A message with neither header is `400`. An empty `subject` becomes `Re:` and the original subject. `DELETE /v1/inboxes/{id}/messages/{messageId}` removes that message.

Create and send take an optional `Idempotency-Key` header. Repeating a completed key returns the first result and does not create or send again. `GET /v1/audit` lists operator actions that created an agent, changed a grant, suspended an agent, or created or deleted an inbox. `after` is the last id the caller has seen. The response includes `next` when another page remains. An agent token is `403`. The log does not contain secrets or message bodies. When `INBAI_DATABASE_URL` is set, the log is in PostgreSQL. Without that URL it stays in the process.

The server's product defaults allow 50 submitted messages per inbox per UTC day, 500 per sender domain per UTC day (the domain of the address the message is sent as), 5000 per workspace per UTC day, and 30 per recipient domain per UTC minute. An operator can adjust those limits with `INBAI_QUOTA_INBOX_DAILY`, `INBAI_QUOTA_DOMAIN_DAILY`, `INBAI_QUOTA_WORKSPACE_DAILY`, and `INBAI_RATE_RECIPIENT_PER_MINUTE`. The next send is `429`. A failed submit does not consume the quota. When `INBAI_DATABASE_URL` is set, the counters are in PostgreSQL and stay there after `inbaid` restarts. Without that URL they stay in the process. `GET /v1/retention` and `PUT /v1/retention` read and set how many days mail is kept. Zero keeps it. `POST /v1/retention/run` deletes mail older than that period. A message with no received time is kept.

Errors use `application/problem+json` with `type`, `title`, `status`, and `detail`. A `400`, `403`, `404`, `409`, `429`, `501`, or event-store `503` says why in `detail`. When the mail server refuses a send or a delete, the response is `502` and `detail` is `mail server refused the request:` followed by the server's reason, such as `overQuota` or `forbiddenFrom`. Any other failure is `502` with `detail` `internal error, see the server log for request id <id>`. The id is the response's `X-Request-Id`, and the operator finds the cause on that request's log line. An MCP tool error shows the same text.

The token form at `POST /oauth/token` is at most 4 KiB. A larger body is `413` with `{"error":"invalid_request"}`.

## Exchange a client credential

An agent obtains an access token from `POST /oauth/token` using `grant_type=client_credentials`. The server accepts the client id and secret either in HTTP Basic auth (`client_secret_basic`) or in the form body (`client_secret_post`):

```sh
curl -s -X POST \
  -u "$INBAI_CLIENT_ID:$INBAI_CLIENT_SECRET" \
  -d 'grant_type=client_credentials' \
  http://127.0.0.1:8787/oauth/token
```

The response is JSON with `access_token`, `token_type` (`bearer`), and `expires_in` (`600` seconds). Pass `access_token` in `Authorization: Bearer <token>` on `/v1/*` and `/mcp` calls, and request a fresh token before it expires. If the credential is unknown or the agent has been suspended, `POST /oauth/token` answers `401` with `{"error":"invalid_client"}`.

Discovery metadata is served at `GET /.well-known/oauth-authorization-server` and `GET /.well-known/oauth-protected-resource`.
