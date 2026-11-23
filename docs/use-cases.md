Status: partial — an operator can provision an inbox, and an agent can authenticate and call the API or MCP. Live delivery to another server is not proven on this page.

# Use cases

inbAI is a control plane in front of a mail server. An operator provisions domains and inboxes. An agent uses the REST API or the MCP server. A grant decides what that agent may do. `inbai init` can bootstrap the mail server, and the operator CLI can create an inbox and submit or list mail. `inbaid serve` exposes those operations to an operator token and to an agent access token. MCP is served at `/mcp` and by `inbai mcp serve`.

## One inbox per agent

An operator creates an inbox for an agent and grants that agent `read`, `send`, and `reply` on it. The agent sends and receives as that address through its own credential. The credential is an OAuth client ID and secret issued by `inbaid`. The access token names the caller. Each request reads the grant from PostgreSQL, so revoking the grant stops the agent on the next call.

Creating domains and inboxes stays with the operator. An agent can create an inbox when it also holds `inbox:provision`. Stalwart credentials stay on the server.

## Several agents, one inbox

A grant is a tuple of agent, inbox, and permissions. The permissions are `read`, `send`, `reply`, `delete`, and `manage`. One inbox can be granted to many agents. One agent can hold many inboxes.

An operator can grant one agent `read` on an inbox and grant another agent `reply` and `send` on the same inbox. Each agent can use the inboxes and permissions named in its own grants.

## Read next

- [Architecture](architecture.md)
- [Agents that use inbAI](agents/overview.md)
