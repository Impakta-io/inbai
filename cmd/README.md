Status: current

# Commands

`inbai` is the operator CLI. With no arguments, or with `help`, it lists its commands. The command tables in `inbai/commands.go` and `inbaid/commands.go` drive dispatch, help, and the [CLI reference](../docs/reference/cli.md). Their shared code is in `internal/cli`. `inbai init` bootstraps Stalwart, `inbai domain add` prints that domain's DNS records, and `inbai doctor` checks the host. The other commands are listed in the [CLI guide](../docs/guides/cli.md). `inbaid` is the server. `inbaid serve` runs the REST API and MCP, and `inbaid version` prints its version.

Shared version text lives in `internal/version`.
