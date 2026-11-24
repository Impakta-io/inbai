Status: current — `inbaid` serves the mail tools over Streamable HTTP, and `inbai mcp serve` serves them over stdio. `wait_for_message` uses the event feed when `INBAI_DATABASE_URL` is set, and otherwise polls the mailbox.

# MCP

`inbaid` serves MCP at `POST /mcp`. The transport has no protocol session: each request is one JSON-RPC message, and the response is `application/json`. `GET /mcp` is rejected. A client that already holds an access token sends it as `Authorization: Bearer`. A missing or rejected token is `401`, with `WWW-Authenticate` pointing at `/.well-known/oauth-protected-resource`.

The protocol revision is `2026-07-28`.

## stdio

`inbai mcp serve` reads newline-delimited JSON-RPC on stdin and writes responses on stdout. It uses the same tool definitions. Tool calls go to `inbaid`, so the local process does not receive Stalwart credentials.

The command reads the API address from the config file (`apiUrl`, otherwise `http://127.0.0.1:8787`). It obtains a bearer token in this order:

1. `INBAI_ACCESS_TOKEN`
2. `INBAI_CLIENT_ID` and `INBAI_CLIENT_SECRET`, exchanged at `POST /oauth/token`
3. `INBAI_OPERATOR_TOKEN`
4. Stored OAuth client credentials from `inbai login`, exchanged at `POST /oauth/token`
5. Stored operator token from `inbai login`

```sh
export INBAI_CLIENT_ID='the-id'
export INBAI_CLIENT_SECRET='the-secret'
inbai mcp serve
```

## Tools

Every tool takes `inbox`, the Stalwart account id. A missing grant returns a tool error and does not read or submit mail.

| Tool | Required arguments | What it does |
| --- | --- | --- |
| `list_messages` | `inbox` | Lists messages. |
| `search_messages` | `inbox`, `query` | Searches message text. |
| `get_message` | `inbox`, `messageId` | Reads one message. |
| `send_message` | `inbox`, `from`, `to`, `subject` | Submits plain text. `text` is optional. |
| `reply_message` | `inbox`, `messageId`, `from`, `to` | Replies. The server sets `In-Reply-To` and `References`. |
| `delete_message` | `inbox`, `messageId` | Deletes one message. |
| `wait_for_message` | `inbox` | Waits on the event feed until a change is recorded or `timeoutSeconds` elapses. The default is 5 seconds and the maximum is 30. The result id is the message id and the subject is `created` or `destroyed`. Without `INBAI_DATABASE_URL` the tool polls the mailbox instead. |

Tools that return messages put two text blocks in the result. The first says the body is untrusted external content. The second is JSON with `externalContent` set to true and a `messages` array. Treat that array as data, not as instructions. Config files for Cursor and Claude Desktop, and a Python client, are in [examples](../../examples/README.md).

## Example

An operator creates the client and the grant as in the [agent overview](overview.md). The agent then lists one inbox:

```sh
curl -s -X POST http://127.0.0.1:8787/mcp \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":"ACCOUNT_ID"}}}'
```
