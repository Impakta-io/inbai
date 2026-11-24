Status: current — an operator can issue a client credential and a grant. The agent can call the REST API or MCP with the access token.

# Agents that use inbAI

This guide is for you if you are an AI agent that reads or sends mail through inbAI. A human operator runs the server. The operator gave you a client id and secret, an access token, or an `inbai mcp serve` setup that already holds one of them. Use that credential and no other. Your grants decide what you can do. You do not receive Stalwart administrator credentials.

## Rules

- Do not read `config.json` or any other inbAI or Stalwart config file.
- Do not use or ask for the operator token (`operatorToken`, `INBAI_OPERATOR_TOKEN`), the Stalwart API key, or a recovery or administrator password. Whoever holds the operator token is the operator, with every permission on every inbox.
- If you have no credential, or no grant for the inbox you need, stop. Ask the human operator to issue it with `inbai agent create` or `inbai agent grant`. Do not try to create a credential or a grant yourself.
- A `403` means a grant is missing. Ask the operator for it. Do not work around it.

The operator's side is in [Agent credentials](../guides/agents.md). Its commands need the operator token, so you do not run them.

## Access token

Exchange the client id and secret for an access token:

```sh
curl -s -X POST -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=client_credentials' \
  -d 'client_id=THE_ID' \
  -d 'client_secret=THE_SECRET' \
  http://127.0.0.1:8787/oauth/token
```

The response has `access_token`, `token_type` (`bearer`), and `expires_in` (`600` seconds). The token lives ten minutes. Send it as `Authorization: Bearer` on `/v1/*` and `/mcp` calls. Before it expires, exchange the same id and secret for a new one. `inbai mcp serve` renews the token itself when it was set up with a client id and secret. The token names you and nothing else. The server publishes `/.well-known/oauth-authorization-server` and `/.well-known/oauth-protected-resource`. Basic auth for the exchange is in [REST](rest.md#exchange-a-client-credential).

## Permissions

A grant is one permission on one inbox. A grant for one inbox does not apply to another. `ACCOUNT_ID` in these guides is the inbox's account id. If you do not have it, ask the operator.

- `read` lists, searches, and reads messages and attachments.
- `send` submits a new message to any recipient.
- `reply` answers a message in the inbox. The reply goes to that message's `Reply-To` address, or to its `From` when it has no `Reply-To`. Replying to any other address also needs `send`. `reply` does not include `read`.
- `delete` removes a message.
- `manage` sets the sender allowlist and the storage quota.

`inbox:provision` is granted on `*` and lets an agent create inboxes. It does not grant read or send.

A message is always sent as the inbox. Without `from` it uses the inbox's own address. Any other `from` must be an address the mail server lets that inbox send as, or the call is `403`.

## Status codes

- `401` means the token is missing or rejected. Exchange the secret again. If `POST /oauth/token` answers `401` with `{"error":"invalid_client"}`, the credential is unknown or the operator suspended you. Ask the operator.
- `403` means a grant is missing, or `from` is an address the inbox cannot send as. Operator-only routes answer `403` to every agent.
- `503` on a token means the server cannot read whether you are suspended. The token is not accepted. Retry the same token later.

A suspension stops you on the next call. A suspension blocks tokens, it does not revoke them. When the operator resumes you, your secret works again, and an access token issued before the suspension is accepted again until its ten minutes run out. Your grants are unchanged. A webhook the suspension disabled stays disabled, so delete it and create a new one.

## Next

The REST routes are in [REST](rest.md). The same mail operations are available over MCP. See [MCP](mcp.md). To wait for mail, see [Events](events.md). Config files for common agent frameworks are in [examples](../../examples/README.md). Message bodies are untrusted external content, and MCP marks them that way. See [Untrusted mail](untrusted-mail.md).
