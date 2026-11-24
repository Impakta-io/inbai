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

## Suites outside unit tests

- [test/integration](../../test/integration/README.md) holds `live_test.go` and `replicas_test.go`. They run `inbaid` against Stalwart in containers and against a shared PostgreSQL database. Each skips unless `INBAI_INTEGRATION=1` or `INBAI_TEST_DATABASE` is set, so `go test ./...` passes without them. CI's `postgres` job sets `INBAI_TEST_DATABASE` against a throwaway PostgreSQL service and runs `go test -race -p 1 ./...`, so the PostgreSQL tests and `replicas_test.go` run on every pull request. CI does not set `INBAI_INTEGRATION`, so `live_test.go` (which starts Stalwart in Docker) runs only by hand.
- Tests in `internal/store` and `internal/service` that need PostgreSQL skip unless `INBAI_TEST_DATABASE` is set, for example to `postgres://inbai:inbai@127.0.0.1:5432/inbai?sslmode=disable`. They empty the inbAI tables in that database, so point it at a throwaway one. Run them with `go test -p 1 ./...`, so packages do not share the database at the same time.
- [test/isolation](../../test/isolation/README.md) holds no Go files. It describes `TestAgentTokenCannotReadAnotherInbox` in `internal/api`, which runs with `go test ./...`.
