Status: planned — filled in when the MCP server exists.

# MCP

`inbaid` will serve MCP over Streamable HTTP at `/mcp`. `inbai mcp serve` will serve the same tools over stdio for clients that only launch a local process. Neither command exists yet.

## Planned sections

- The HTTP endpoint and the metadata a client uses to discover authorization.
- The stdio command and the environment it reads.
- The tool list, with arguments and errors.
- How a tool result marks a message body as external content.
- A short example an agent can follow without a human in the loop.
