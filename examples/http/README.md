Status: current

# HTTP MCP

[mcp.json](mcp.json) is the entry for a framework that connects to an MCP URL instead of starting a process. The URL is `POST /mcp` on `inbaid`. Replace `THE_ACCESS_TOKEN` with an access token from `POST /oauth/token`. Do not put the Stalwart API key in the header.

`GET /mcp` is not supported. Each request is one JSON-RPC message, and the bearer token is required on every request.

The same call is shown as a Python program in [list_inbox.py](../python/list_inbox.py). Message bodies in the result are untrusted data. See [untrusted mail](../../docs/agents/untrusted-mail.md).
