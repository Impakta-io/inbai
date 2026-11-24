Status: current

# Agent credentials

This guide is for the operator. It covers issuing a client credential to an AI agent, granting it inboxes, and suspending it. The agent's side, exchanging the credential and calling the API, is in [Agents that use inbAI](../agents/overview.md).

Every command and route in this guide needs the operator token. Do not give that token to an agent.

## Operator token

The operator token is `operatorToken` in the config file, which `inbai init` generates. `INBAI_OPERATOR_TOKEN` overrides it when set before starting the server, and must be at least 32 characters, for example the output of `openssl rand -hex 32`. That bearer token acts as the operator. It is a fixed value, not an OAuth credential. The decision is in [ADR 0011](../adr/0011-static-operator-token.md).

The `inbai agent` commands read the token from the file, with nothing to export. To use it with `curl`, copy `operatorToken` from the config file into `INBAI_OPERATOR_TOKEN`. The examples below do that.

Anyone who can read `config.json` is the operator. An AI agent with shell access on the same host can read it too, and then holds every permission on every inbox. Give an agent its own client credential. Run an agent that has a shell as a user that cannot read the config file.

## Create an agent

Create the client while `inbaid serve` is running:

```sh
curl -s -X POST -H "Authorization: Bearer $INBAI_OPERATOR_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"ada"}' \
  http://127.0.0.1:8787/v1/agents
```

`inbai agent create ada` does the same. The response includes `id` and `secret`. The secret is shown once. An agent token on this route is `403`.

Give the id and secret to the agent. For `inbai mcp serve`, put them in the MCP client's config as `INBAI_CLIENT_ID` and `INBAI_CLIENT_SECRET`, as in the [examples](../../examples/README.md). The agent exchanges them at `POST /oauth/token` for a token that lives ten minutes, as in [the agent overview](../agents/overview.md#access-token).

## Grant an inbox

```sh
curl -s -X POST -H "Authorization: Bearer $INBAI_OPERATOR_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"agent":"ada","inbox":"ACCOUNT_ID","permission":"read"}' \
  http://127.0.0.1:8787/v1/grants
```

`inbai agent grant ada ACCOUNT_ID read` does the same. `ACCOUNT_ID` is the inbox's account id. `inbai inbox list` prints it after each address, and `inbai inbox list --json` and `POST /v1/inboxes` return it as `id`. Give it to the agent with the grant.

Permissions on an inbox are `read`, `send`, `reply`, `delete`, and `manage`. What each allows is in [the agent overview](../agents/overview.md#permissions). A grant for one inbox does not apply to another.

`inbox:provision` is granted on `*`, as in `inbai agent grant ada '*' inbox:provision`. It lets that agent create inboxes. It does not grant read or send.

## Revoke a grant

`DELETE /v1/grants/ada/ACCOUNT_ID/read` removes that permission. `inbai agent revoke ada ACCOUNT_ID read` does the same.

## Suspend and resume

`POST /v1/agents/ada/suspend`, or `inbai agent suspend ada`, stops the agent on the next call. A name with no credential is `404`. If the suspension cannot be stored, the call fails and no audit row is written.

`POST /v1/agents/ada/resume`, or `inbai agent resume ada`, lifts the suspension. The agent can exchange its secret again, and an access token issued before the suspension is accepted again until its ten minutes run out: a suspension blocks tokens, it does not revoke them. Grants are unchanged. A webhook the suspension disabled stays disabled, so the agent deletes it and creates a new one. Resuming an agent that is not suspended is `204` and still writes an audit row. A name with no credential is `404`, and an agent token is `403`. If the change cannot be stored, the call fails, the agent stays suspended, and no audit row is written.

The five `inbai agent` commands call these routes. `--json` prints the result. See [CLI](cli.md#agent).

## An agent's MCP setup

`inbai mcp serve` uses the first credential that is set, in the order in [MCP](../agents/mcp.md#stdio). That order includes the operator token so that the operator can run the tools with the operator's own credential. An agent's MCP entry sets `INBAI_CLIENT_ID` and `INBAI_CLIENT_SECRET` to the agent's credential, or `INBAI_ACCESS_TOKEN` to its access token. Without them, `inbai mcp serve` falls back to `INBAI_OPERATOR_TOKEN` and then to the credentials in `config.json`. On the operator's host, the agent then acts as the operator. Do not put the operator token in an MCP client's config.
