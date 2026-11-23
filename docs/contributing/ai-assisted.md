Status: current

# Contributing with AI

Using an AI tool is optional. Nobody has to use one, and using one does not lower the bar. The checks in CI are the same either way.

You are the author of anything you submit. Review it, run it, and be able to explain it. A change you cannot debug is not ready.

## Workflow

1. Start from an issue. For a large change, open a [proposal](proposals.md) first.
2. Keep the diff small enough for a person to review in one sitting.
3. Run `make build test lint docs-check`.
4. Read every line of the diff. Delete anything you cannot explain.
5. Commit with a `Signed-off-by` trailer. See [CONTRIBUTING.md](../../CONTRIBUTING.md).
6. Disclose material AI assistance, then open the pull request yourself.

A coding agent that edits this repository should be pointed at [AGENTS.md](../../AGENTS.md). That file has the commands, the directory map, and the rules.

## What to disclose

Disclose assistance when a tool wrote a material part of the code, the tests, or the docs. Add a trailer to the commit:

```text
Assisted-by: Tool:model
```

Examples:

```text
Assisted-by: Claude Code:claude-opus-4.8
Assisted-by: Cursor:composer
```

Name the product and the model. More than one trailer is fine.

Spellcheck and single-line completion need no trailer. If you are unsure, add the trailer.

The pull request template asks you to tick whether a tool wrote a material part of the change.

## What reviewers will ask

- What does this change do, in your own words?
- Which test shows the new behavior?
- Why this approach, if the diff is larger than the issue needs?
- Which license covers any code a tool inserted?

Reviewers may close a pull request when reviewing it would take longer than writing the change. That decision is about the diff, not about the tool.

## What stays out

- Do not present generated text you have not read.
- Do not copy Stalwart source, or any other code that cannot be licensed Apache-2.0, into this repository.
- Do not let an unattended bot open or merge pull requests.
