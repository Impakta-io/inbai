Status: current

# ADR 0002: Apache-2.0 and the Developer Certificate of Origin

- Status: accepted
- Date: 2026-11-23

## Context

inbAI should be usable by individuals and companies, and contributors need a simple way to state that they have the right to submit their work.

## Decision

Code, docs, and other files in this repository are Apache-2.0. Every commit carries a `Signed-off-by` trailer under the Developer Certificate of Origin 1.1. Stalwart stays a separate process under its own license and is not vendored here.

## Consequences

The project stays permissive. Contributors certify their submissions. Combining this tree with AGPL code would be a new decision, not an accident of a dependency.
