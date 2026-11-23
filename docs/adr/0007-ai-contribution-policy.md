Status: current

# ADR 0007: One standard for work with and without AI

- Status: accepted
- Date: 2026-11-23

## Context

Contributors will arrive with and without AI tools. The project should welcome both, and reviewers need to know who stands behind a change.

## Decision

AI assistance is optional and never required. The same review standard and the same CI checks apply either way. The human who opens the pull request is the author, signs off under the Developer Certificate of Origin, and can explain the change. Material AI assistance is disclosed with an `Assisted-by: Tool:model` trailer. Unattended bots do not get merge access. The instructions for coding agents live in `AGENTS.md`.

## Consequences

People can use the tools they already use. Reviewers can reject a diff that the author cannot explain. The policy has to be kept in sync with `CONTRIBUTING.md` and `docs/contributing/ai-assisted.md`.
