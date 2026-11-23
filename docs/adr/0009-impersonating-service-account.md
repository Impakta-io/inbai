Status: current

# ADR 0009: One impersonating service account

- Status: accepted
- Date: 2026-11-23

## Context

inbAI has to read and send mail for many inboxes without giving those credentials to agents. Stalwart can allow one account to act as another. That concentrates access, so the inbAI grant check is the whole isolation boundary. A spike had to show that a role narrower than full administrator is enough.

## Decision

`inbaid` holds one Stalwart service account with the `impersonate` permission and addresses mailboxes by account id. The account uses a custom role, not the built-in administrator role.

The spike on Stalwart 1.1.1 confirmed this. Authenticated only with that account's API key, the caller created inboxes, listed another account's mailboxes, and submitted mail as that account. Stalwart refused a recipient until the inbox role also included `emailReceive`. The service role must include every permission the inbox role grants, because Stalwart will not let a caller create an account more powerful than itself.

The fallback, one stored credential per inbox, is not needed.

## Consequences

Agents never see Stalwart credentials. A bug in the inbAI grant check exposes every inbox. The inbox role and the service role must both include `emailReceive`, or delivery fails with "not authorized to receive email." The isolation suite has to run in CI once mail exists.
