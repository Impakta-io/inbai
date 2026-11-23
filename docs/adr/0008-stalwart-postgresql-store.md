Status: current

# ADR 0008: Stalwart data store on PostgreSQL

- Status: proposed
- Date: 2026-11-23

## Context

Stalwart's default single-node store cannot be shared by a second node. Starting there means a migration before the mail service can grow. This choice is not accepted until a spike sends and receives real mail with Stalwart on PostgreSQL and records the cost.

## Decision

The working plan is that Stalwart uses PostgreSQL for its data store, in its own database, and uses the local filesystem for blobs unless the operator selects S3-compatible storage.

## Consequences

A second Stalwart node can be added later without moving the mail store. Single-node throughput may be lower than the embedded default. The spike measures that before this record is accepted.
