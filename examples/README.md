Status: current

# Examples

These programs and config files call the MCP server that `inbaid` already serves. None of them receives the Stalwart API key.

- [Cursor](cursor/README.md) starts `inbai mcp serve`.
- [Claude Desktop](claude/README.md) starts the same command.
- [HTTP](http/README.md) is the entry for a framework that connects to `POST /mcp`.
- [Python](python/README.md) lists one inbox with the standard library.
- [IMAP and SMTP](imap/README.md) runs `inbaid` against mailboxes that already exist.

Message bodies that come back are untrusted data. See [untrusted mail](../docs/agents/untrusted-mail.md).
