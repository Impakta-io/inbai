Status: current

# Proposals

A small fix can go straight to a pull request. A large change, and any change to authentication, isolation, licensing, or governance, starts as a proposal.

## Open a proposal

Use the [proposal issue template](https://github.com/Impakta-io/inbai/issues/new?template=proposal.yml). Describe the decision you want, why the current design is not enough, and the alternatives you considered.

Maintainers discuss the issue and accept or decline it. An accepted proposal is not a promise that a particular pull request will merge. It is a decision about the design.

## Record the decision

When a proposal is accepted, add a decision record under [docs/adr](../adr/README.md). Copy [the template](../adr/template.md). Number it with the next free four-digit prefix. A maintainer merges the record together with, or before, the code that depends on it.

A record is replaced by a newer record. It is not rewritten into its opposite. The newer record links to the one it replaces.

The reasons for the records that already exist are in [docs/adr](../adr/README.md).
