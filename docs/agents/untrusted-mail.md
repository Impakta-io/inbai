Status: planned — filled in when message reads return authentication results.

# Untrusted mail

Mail from outside the system is untrusted input to a language model. The API does not return messages yet. When it does, this page will tell an agent how to treat them.

## Planned sections

- Which fields carry SPF, DKIM, DMARC, and the spam verdict.
- How MCP marks a message body as external content.
- The optional sender allowlist on an inbox.
- What these signals do not do. They do not, by themselves, make a message safe to follow as instructions.
