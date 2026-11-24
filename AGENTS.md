Status: current

# Instructions for coding agents

These instructions are for an agent editing this repository. An agent that sends or receives mail follows [docs/agents](docs/agents/overview.md).

An agent never reads `config.json` or any other inbAI or Stalwart config file, and never uses the operator token or the Stalwart API key. Whoever holds the operator token is the operator, with every permission on every inbox. If you need a credential or a grant, stop and ask the human operator to issue it with `inbai agent create` or `inbai agent grant`.

## Commands

From the repository root, with Go 1.27.4 or newer:

```sh
make build
make test
make lint
make fmt
make docs-check
```

`make lint` needs `golangci-lint` v2.15.0 on `PATH`. Install it with:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
```

On Windows, `make` may be missing. Use:

```sh
go build -o bin/inbai.exe ./cmd/inbai
go build -o bin/inbaid.exe ./cmd/inbaid
go test ./...
go run ./tools/reference -check
go run ./tools/docscheck
golangci-lint run ./...
```

A change is done when those commands pass, the diff is small enough for a person to review, the human author can explain it, and a pull request is open.

## Where code goes

| Path | Owns |
| --- | --- |
| `cmd/inbai` | Operator CLI. It calls the API. It does not enforce grants. |
| `cmd/inbaid` | Server process. It wires transports, the service layer, and workers. |
| `internal/api` | REST transport. |
| `internal/mcp` | MCP transport, HTTP and stdio. |
| `internal/service` | Grants, quotas, and other policy. This is the isolation boundary. |
| `internal/auth` | Credentials and token verification. A token names the caller only. |
| `internal/mailbackend` | The mail interface. |
| `internal/mailbackend/stalwart` | The Stalwart implementation of that interface. |
| `internal/mailbackend/imap` | The IMAP and SMTP implementation, used with `INBAI_MAIL=imap`. |
| `internal/store` | PostgreSQL state. Message bodies do not belong here. |
| `internal/jobs` | Background jobs. |
| `internal/events` | Cursors, the event feed, and webhooks. |
| `internal/config` | Process configuration. |
| `internal/doctor` | Host checks for `inbai doctor`. |
| `internal/version` | The version string both binaries print. |
| `internal/load` | The measurement run behind `tools/load`. |
| `api/openapi.yaml` | The HTTP contract. Change this before the client or the handlers. |
| `tools/docscheck` | Checks public Markdown. |
| `tools/reference` | Writes the CLI and HTTP reference pages. |
| `tools/load` | Runs a load measurement against a running `inbaid`. |
| `docs` | Documentation. A behavior change updates the doc in the same change. |

Each `internal` package has a `doc.go` that states its job. Read it before editing the package.

## Branches and pull requests

Do not commit on `main`. Do not push to `main`. GitHub rejects a push to `main`. Every change is a branch and a pull request into `main`.

Name the branch `<type>/<summary>`.

- `type` is one of `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, or `spike`.
- `summary` is two to five lowercase words joined by hyphens.
- Do not add a username, a ticket number, or an `agent/` prefix.

Examples: `docs/agent-branch-naming`, `feat/inbox-create`, `fix/grant-check`.

```sh
git fetch origin
git switch main
git pull origin main
git switch -c docs/agent-branch-naming
```

Commit on that branch. Each commit has `Signed-off-by`. Push and open the pull request before calling the work finished:

```sh
git push -u origin HEAD
gh pr create --base main
```

One branch is one pull request. If work was started on `main` by mistake, move it onto a new branch before the first push.

## Rules

- Keep the change small. One issue, one pull request.
- Add a test for every behavior change. A documentation-only change does not need a Go test.
- Do not add a dependency without an issue a maintainer has accepted.
- Do not import, copy, or vendor Stalwart source. Talk to Stalwart over JMAP from `internal/mailbackend/stalwart` only.
- Do not put grant checks in a transport. `internal/service` decides access. See [internal/service/AGENTS.md](internal/service/AGENTS.md).
- Do not store secrets in the repository.
- Public Markdown needs a `Status:` line. Run `make docs-check`.
- Do not mention gitignored local planning notes in a public file.
- Write docs for what exists. A feature that is not built yet is an outline with `Status: planned`.

## Ask a maintainer first

Open an issue and wait before you:

- add a dependency
- change `internal/service` or `internal/auth`
- change `.github/workflows`
- change the license, the governance file, or the contribution policy

## Disclosure

The human who opens the pull request is the author.

- Add `Signed-off-by: Name <email>` to every commit.
- When an AI tool writes a material part of the change, add `Assisted-by: Tool:model`.
- Spellcheck and single-line completion need no trailer.
- Do not open a pull request for a change you cannot explain or debug.

The policy is in [docs/contributing/ai-assisted.md](docs/contributing/ai-assisted.md).
