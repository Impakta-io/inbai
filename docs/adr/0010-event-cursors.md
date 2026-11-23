Status: current

# ADR 0010: Event cursors, with webhooks as hints

- Status: proposed
- Date: 2026-11-23

## Context

Agents need to learn about new mail, including agents that cannot accept inbound connections. Stalwart can POST ingest events, and it drops events it cannot deliver after a short time. A hint alone would lose mail while `inbaid` is down.

## Decision

The working plan is a JMAP state cursor per inbox, stored in PostgreSQL. A worker asks Stalwart for changes since that cursor and advances it in the same transaction as the recorded event. A Stalwart ingest webhook only prompts the worker to poll. Consumers receive signed webhooks or long-poll `GET /v1/events`. If the ingest event does not identify the recipient, the fallback is JMAP push.

## Consequences

Stopping `inbaid` does not drop mail events. Delivery is at least once. The spike has to confirm what the ingest event contains before this record is accepted.
