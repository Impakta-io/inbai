Status: current

# ADR 0009: One impersonating service account

- Status: proposed
- Date: 2026-11-23

## Context

inbAI has to read and send mail for many inboxes without giving those credentials to agents. Stalwart can allow one account to act as another. That concentrates access, so the inbAI grant check becomes the whole isolation boundary. A spike has to show that a narrow role is enough.

## Decision

The working plan is that `inbaid` holds one Stalwart service account with the `impersonate` permission and addresses mailboxes by account ID. The role is limited to account management, domain management, and mail access. Stalwart's client listeners are not public. If the spike cannot get a narrow enough role, the fallback is one credential per inbox, encrypted in PostgreSQL.

## Consequences

Agents never see Stalwart credentials. A bug in the grant check exposes every inbox. The isolation suite has to run in CI once mail exists.
