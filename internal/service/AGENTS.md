Status: current

# Agents working in internal/service

This package is the isolation boundary. A caller who passes a check here can read or send mail for an inbox. A mistake here is a privacy bug.

Before changing this package:

- Open an issue and wait for a maintainer to agree the change. Do this even for a small edit.
- Add a test that fails when the grant check is skipped, weakened, or applied to the wrong inbox.
- Keep the decision in this package. Do not move a permission check into `internal/api` or `internal/mcp`.
- Read [AGENTS.md](../../AGENTS.md) for the repository rules. This file adds to those rules.
