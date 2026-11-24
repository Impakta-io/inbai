Status: current

# Tests

Unit tests live next to the packages they cover. Run them with `go test ./...`.

[integration](integration/README.md) holds two Go tests that run against real services and skip unless an environment variable is set. [isolation](isolation/README.md) describes the grant-isolation test, which lives in `internal/api`. See [testing.md](../docs/contributing/testing.md).
