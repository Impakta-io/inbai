Status: current

# Mail backends

The service layer calls `internal/mailbackend`. A transport does not call a mail system, and a backend does not decide whether an agent may read an inbox.

## Boundary

- Call the mail system over its network API. Do not import, copy, or vendor its source.
- Stalwart is reached over JMAP from `internal/mailbackend/stalwart`. Its Community Edition is AGPL-3.0 and stays in its own process.
- `internal/mailbackend/imap` reads mail over IMAP and submits it over SMTP. It does not provision accounts. `inbaid serve` uses it when `INBAI_MAIL=imap`. The account file and the addresses are described in [examples/imap](../../examples/imap/README.md).
- Grants are checked before the backend is called.

## Adding a backend

A new backend is a proposal, then an implementation of the interface. Gmail and Microsoft 365 are not included. Put a usage note under `examples/`.

An inbox create passes an `InboxSpec`: the local part and the storage quota. Anything else the mail server needs to make the account is the backend's own configuration, set when `cmd/inbaid` builds it. The Stalwart backend takes its domain id and inbox role id that way.

The contribution has to be licensable as Apache-2.0. Code copied from a mail server that uses a different license cannot be added here.
