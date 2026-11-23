Status: current

# ADR 0008: Stalwart data store on PostgreSQL

- Status: accepted
- Date: 2026-11-23

## Context

Stalwart's default single-node store cannot be shared by a second node. Starting there means a migration before the mail service can grow. The choice needed a running server, not only the documentation.

## Decision

Stalwart uses PostgreSQL for its data store, in its own database. Blobs stay in that database unless an operator later selects filesystem or S3 storage.

A spike on 23 November 2026 ran Stalwart 1.1.1 (`stalwartlabs/stalwart:v1`) under Compose against PostgreSQL 16. The same server delivered a message into a local mailbox and submitted another through an authenticated SMTP relay. Both deliveries returned success. Creating 10,000 accounts through the management API took 174 seconds, in batches of 200.

A comparison with the embedded store was not run.

## Consequences

A second Stalwart node can be added later without moving the mail store. Account creation at this size is fast enough for bulk inbox provisioning. Blob growth will make the PostgreSQL database larger until a separate blob store is configured.
