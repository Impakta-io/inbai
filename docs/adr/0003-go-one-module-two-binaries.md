Status: current

# ADR 0003: Go, one module, two binaries

- Status: accepted
- Date: 2026-11-23

## Context

Operators need a client on macOS, Linux, and Windows. The server is a long-running process. Contributors are more likely to know Go than the language of the mail server.

## Decision

The module is `github.com/Impakta-io/inbai`. It builds two binaries: `inbai`, the CLI, and `inbaid`, the server. They share the packages under `internal`.

## Consequences

One toolchain covers the client and the server. The CLI can be cross-compiled in CI. The mail server remains a separate program.
