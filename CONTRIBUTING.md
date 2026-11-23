Status: current

# Contributing

inbAI is a community project. Contributions of code, tests, docs, examples, reviews, and operational notes are welcome. You do not need to be an email expert. Say what you know and what you are unsure about.

AI assistance is optional. It is never required. Work done with AI and work done without it meet the same standard and pass the same checks.

## Ways to contribute

- [Report a bug](https://github.com/Impakta-io/inbai/issues/new?template=bug.yml) or [suggest a feature](https://github.com/Impakta-io/inbai/issues/new?template=feature.yml).
- [Propose a design change](docs/contributing/proposals.md) before a large pull request.
- Improve the docs. Outlines marked `Status: planned` describe work that is not built yet.
- Add a mail backend, an agent-framework example, or a deployment recipe once those areas exist. The boundary is in [mail backends](docs/contributing/mail-backends.md).

Security problems go through [SECURITY.md](SECURITY.md), not a public issue.

## Branches

Do not commit on `main`. Create a branch named `<type>/<summary>` and open a pull request into `main`.

`type` is one of `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, or `spike`. `summary` is two to five lowercase words joined by hyphens, as in `feat/inbox-create`. Coding agents follow the same rule. The steps are in [AGENTS.md](AGENTS.md).

## What every contribution needs

- It solves a problem described in an issue or in the pull request.
- A person can review it in one sitting.
- A behavior change has a test, or the pull request describes a manual check.
- Stalwart source and other code that cannot be licensed Apache-2.0 stays out of this repository.
- The human who submits it understands it and can debug it.

## Without AI

Write the change, explain why in the pull request, and certify it with a `Signed-off-by` trailer.

## With AI

You may use assistants and coding agents.

- You are the author. Review the change, run it, and be ready to explain it.
- Disclose material assistance with an `Assisted-by: Tool:model` trailer. Spellcheck and single-line completion need no disclosure.
- A human opens the pull request. Unattended bots do not get merge access.
- Do not submit a change you cannot debug.
- Maintainers may close a pull request when reviewing it would take longer than writing the change.

The workflow, trailer examples, and what reviewers ask are in [ai-assisted.md](docs/contributing/ai-assisted.md). Coding agents that edit this repository follow [AGENTS.md](AGENTS.md).

## Developer Certificate of Origin

Add this trailer to every commit, using your real name and the email on the commit:

```text
Signed-off-by: Your Name <you@example.com>
```

`git commit -s` adds it. By adding it you certify the [Developer Certificate of Origin 1.1](https://developercertificate.org/):

```text
By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

## Checks

Before you open a pull request, run:

```sh
make build test lint docs-check
```

Continuous integration runs the build and tests on Linux, macOS, and Windows, runs the linters, checks the docs, and rejects a pull request whose commits lack `Signed-off-by`.

## More guides

- [Development setup](docs/contributing/development.md)
- [Testing](docs/contributing/testing.md)
- [Proposals and decision records](docs/contributing/proposals.md)
- [Releases](docs/contributing/releasing.md)
- [Code of conduct](CODE_OF_CONDUCT.md)
