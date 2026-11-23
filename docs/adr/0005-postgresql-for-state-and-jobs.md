Status: current

# ADR 0005: PostgreSQL for inbAI state and jobs

- Status: accepted
- Date: 2026-11-23

## Context

The server must run as more than one replica later, without a rewrite and without a second piece of infrastructure for queues.

## Decision

PostgreSQL stores agents, grants, credentials, domains, cursors, events, webhook subscriptions, jobs, and the audit log. Message bodies stay in the mail backend. Background work uses a PostgreSQL queue. `inbaid` keeps no state of its own between requests.

## Consequences

One database coordinates every replica. Operators who already run PostgreSQL do not add Redis or a message broker. Queue throughput is limited by PostgreSQL, which is acceptable for the first deployment.
