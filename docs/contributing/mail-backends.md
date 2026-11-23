Status: current

# Mail backends

inbAI does not speak SMTP itself. A mail backend does that, or it calls a system that does.

The Go interface is not in the tree yet. It will live in `internal/mailbackend`, and the Stalwart implementation will live in `internal/mailbackend/stalwart`. Until then, these rules are already in force.

## Boundary

- Call the mail system over its network API. Do not import, copy, or vendor its source.
- Stalwart is reached over JMAP only. Its Community Edition is AGPL-3.0 and stays in its own process.
- The service layer calls the interface. CLI, REST, and MCP do not call a mail system directly.
- Grants are checked before the backend is called. A backend does not decide whether an agent may read an inbox.

## Adding a backend

A new backend is a proposal, then an implementation of the interface. Gmail, Microsoft 365, and generic IMAP or SMTP are expected later, and each one is a separate package. Put usage examples under `examples/`.

The contribution has to be licensable as Apache-2.0. Code copied from a mail server that uses a different license cannot be added here.
