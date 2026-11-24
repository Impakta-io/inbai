Status: current — an operator can issue a client credential and a grant. The agent can call the REST API or MCP with the access token.

# Agents that use inbAI

An agent calls the REST API with a credential an operator issued. The access token names the agent. A grant decides which inbox that agent can read, send, reply, delete, or manage. The agent does not receive Stalwart administrator credentials.

## Credential

An operator creates the client while `inbaid serve` is running:

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

An operator can also run `inbai agent create ada`, `inbai agent grant ada ACCOUNT_ID read`, `inbai agent revoke ada ACCOUNT_ID read`, and `inbai agent suspend ada`. Those commands call the same routes. Permissions on an inbox are `read`, `send`, `reply`, `delete`, and `manage`. A grant for one inbox does not apply to another. `inbox:provision` is granted on `*` and lets that agent create inboxes. It does not grant read or send. `DELETE /v1/grants/ada/ACCOUNT_ID/read` removes that permission. `POST /v1/agents/ada/suspend` stops the agent on the next call.

The REST routes are in [REST](rest.md). The same mail operations are available over MCP. See [MCP](mcp.md). Config files for common agent frameworks are in [examples](../../examples/README.md). Message bodies returned by MCP are marked as external content. See [Untrusted mail](untrusted-mail.md).
