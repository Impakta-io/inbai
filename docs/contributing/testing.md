Status: current

# Testing

Run the unit tests with:

```sh
go test ./...
```

`make test` runs the same command.

## What to test

- A behavior change needs a test that fails before the change and passes after it.
- A documentation-only change needs `make docs-check`, not a new Go test.
- Policy in `internal/service` needs a test that shows a caller without a grant is refused, and a caller with a grant is allowed. The stricter rules are in [internal/service/AGENTS.md](../../internal/service/AGENTS.md).

## Suites that are not here yet

Two directories are reserved:

- [test/integration](../../test/integration/README.md) will run the server against Stalwart in containers.
- [test/isolation](../../test/isolation/README.md) will prove that one agent cannot read or send as another.

Both run in CI once they exist. Until then, `go test ./...` does not look inside them, because they hold no Go packages.
