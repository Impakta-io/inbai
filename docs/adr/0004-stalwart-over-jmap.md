Status: current

# ADR 0004: Stalwart as a separate process over JMAP

- Status: accepted
- Date: 2026-11-23

## Context

inbAI needs real mail delivery without implementing SMTP, IMAP, or spam filtering. The mail server also has to be reachable from a language other than its own.

## Decision

Stalwart Community Edition runs as its own process. inbAI calls it over JMAP, for management and for mail. No Stalwart source is imported or vendored. No Enterprise feature is required. The call sits behind the mail backend interface so another provider can be added later.

## Consequences

SMTP and spam filtering stay in Stalwart. inbAI can keep the Apache-2.0 license. The project depends on Stalwart's JMAP behavior, which the validation spike has to confirm.
