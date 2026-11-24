Status: current — an operator can issue a client credential and a grant. The agent can call the REST API or MCP with the access token.

# Agents that use inbAI

An agent calls the REST API with a credential an operator issued. The access token names the agent. A grant decides which inbox that agent can read, send, reply, delete, or manage. The agent does not receive Stalwart administrator credentials.

## Credential

An operator creates the client while `inbaid serve` is running. The examples use the operator token, which `inbai init` stored as `operatorToken` in `config.json`. Export that value as `INBAI_OPERATOR_TOKEN` first, or run the `inbai agent` commands below, which read it from the file:

```sh
curl -s -X POST -H "Authorization: Bearer $INBAI_OPERATOR_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"ada"}' \
  http://127.0.0.1:8787/v1/agents
```

The response includes `id` and `secret`. The secret is shown once. Exchange it for a token that lives ten minutes:

```sh
curl -s -X POST -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=client_credentials' \
  -d 'client_id=THE_ID' \
  -d 'client_secret=THE_SECRET' \
  http://127.0.0.1:8787/oauth/token
```

Send `Authorization: Bearer` with the access token. A missing or rejected token is `401`. The server publishes `/.well-known/oauth-authorization-server` and `/.well-known/oauth-protected-resource`.

## Grant

```sh
curl -s -X POST -H "Authorization: Bearer $INBAI_OPERATOR_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"agent":"ada","inbox":"ACCOUNT_ID","permission":"read"}' \
  http://127.0.0.1:8787/v1/grants
```

`ACCOUNT_ID` is the inbox's account id. `inbai inbox list` prints it after each address, and `inbai inbox list --json` and `POST /v1/inboxes` return it as `id`.

An operator can also run `inbai agent create ada`, `inbai agent grant ada ACCOUNT_ID read`, `inbai agent revoke ada ACCOUNT_ID read`, `inbai agent suspend ada`, and `inbai agent resume ada`. Those commands call the same routes. Permissions on an inbox are `read`, `send`, `reply`, `delete`, and `manage`. A grant for one inbox does not apply to another.

- `read` lists, searches, and reads messages and attachments.
- `send` submits a new message to any recipient.
- `reply` answers a message in the inbox. The reply goes to that message's `Reply-To` address, or to its `From` when it has no `Reply-To`. Replying to any other address also needs `send`. `reply` does not include `read`.
- `delete` removes a message.
- `manage` sets the sender allowlist and the storage quota.

A message is always sent as the inbox. Without `from` it uses the inbox's own address. Any other `from` must be an address the mail server lets that inbox send as, or the call is `403`. This applies to the operator too. `inbox:provision` is granted on `*` and lets that agent create inboxes. It does not grant read or send. `DELETE /v1/grants/ada/ACCOUNT_ID/read` removes that permission. `POST /v1/agents/ada/suspend` stops the agent on the next call. A name with no credential is `404`. If the suspension cannot be stored, the call fails and no audit row is written. `POST /v1/agents/ada/resume` lifts the suspension. The agent can exchange its secret again, and an access token issued before the suspension is accepted again until its ten minutes run out: a suspension blocks tokens, it does not revoke them. Grants are unchanged. A webhook the suspension disabled stays disabled, so the agent deletes it and creates a new one. Resuming an agent that is not suspended is `204` and still writes an audit row. A name with no credential is `404`, and an agent token is `403`. If the change cannot be stored, the call fails, the agent stays suspended, and no audit row is written.

The REST routes are in [REST](rest.md). The same mail operations are available over MCP. See [MCP](mcp.md). Config files for common agent frameworks are in [examples](../../examples/README.md). Message bodies returned by MCP are marked as external content. See [Untrusted mail](untrusted-mail.md).
