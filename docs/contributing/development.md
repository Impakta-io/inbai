Status: current

# Development

## Prerequisites

- Go 1.27.4 or newer
- Git
- `make`, on macOS and Linux

Windows builds use the `go` commands below. `make` is not required there.

## Setup

```sh
git clone https://github.com/Impakta-io/inbai.git
cd inbai
make build test lint docs-check
```

`make lint` needs golangci-lint v2.15.0:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
```

Add `$(go env GOPATH)/bin` to `PATH` if the command is not found. The Makefile adds that directory for its own recipes.

## Windows

```sh
go build -o bin/inbai.exe ./cmd/inbai
go build -o bin/inbaid.exe ./cmd/inbaid
go test ./...
go run ./tools/docscheck
golangci-lint run ./...
```

## What the binaries do

`bin/inbai` prints `inbai 0.0.0-dev`. `bin/inbaid` prints `inbaid 0.0.0-dev`. There are no subcommands yet.

## Where a change goes

Read [AGENTS.md](../../AGENTS.md). It maps each directory to the job it owns.

A behavior change updates the doc that describes it in the same pull request. A feature that is not built yet stays an outline with `Status: planned`.

## Formatting

`make fmt` runs `gofmt`. The editor config in `.editorconfig` matches that.

## Continuous integration

On every push to `main` and on every pull request, CI builds and tests on Linux, macOS, and Windows, runs `make` on Linux, runs golangci-lint v2.15.0, and checks Markdown. A pull request is rejected when a commit has no `Signed-off-by` trailer.
