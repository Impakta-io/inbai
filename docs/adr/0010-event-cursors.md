Status: current

# ADR 0010: Event cursors, with webhooks as hints

- Status: accepted
- Date: 2026-11-23

## Context

Agents need to learn about new mail, including agents that cannot accept inbound connections. Stalwart can POST ingest events, and it drops events it cannot deliver after a short time. A hint alone would lose mail while `inbaid` is down.

## Decision

A JMAP state cursor per inbox, stored in PostgreSQL, is the record of what arrived. A worker asks Stalwart for changes since that cursor. A Stalwart `message-ingest` webhook only prompts the worker to poll. Consumers receive signed webhooks or long-poll `GET /v1/events`.

The spike confirmed that a delivered message produces `message-ingest.ham`. The payload names the recipient in `to` and includes a numeric `accountId`. That number is not the JMAP account id returned when the account is created, so the recipient address is the key used to find the inbox. JMAP push is not required to identify the recipient.

## Consequences

Stopping `inbaid` does not drop mail events, because the cursor can be read later. Delivery to consumers is at least once. The webhook hint must be matched by recipient address, not by treating `accountId` as a JMAP id.
