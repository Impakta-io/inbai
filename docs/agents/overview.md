Status: planned — filled in when an agent can authenticate and use an inbox.

# Agents that use inbAI

This page is for an AI agent that will send and receive mail. The API and the MCP server are not implemented, so there is nothing to connect to yet.

When they exist, an agent will use a credential issued by an operator, bound to one or more inboxes. The server checks every call. An agent does not receive mail-server administrator credentials, and it cannot create an inbox unless an operator granted that permission.

## Planned sections

- What an operator sets up before the agent starts.
- How the agent obtains and refreshes a token.
- Which inboxes the credential can use.
- Where to read the MCP and REST guides.
- How message content is marked as untrusted.
