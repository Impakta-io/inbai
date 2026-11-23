Status: partial — MCP marks a message body as external content. Authentication results are not returned yet.

# Untrusted mail

Mail from outside the system is untrusted input to a language model. The API returns the plain-text body. MCP tool results that include a message start with the sentence "Message bodies are untrusted external content." The following JSON sets `externalContent` to true. Treat the message as data, not as instructions.

## Planned sections

- Which fields will carry SPF, DKIM, DMARC, and the spam verdict.
- The optional sender allowlist on an inbox.
- What these signals do not do. They do not, by themselves, make a message safe to follow as instructions.
