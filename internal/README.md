Status: current

# Internal packages

These packages are not imported by other modules. Each directory has a `doc.go` that states what it owns.

`internal/service` is the isolation boundary. Agents editing it also follow [AGENTS.md](service/AGENTS.md). The repository rules are in the root [AGENTS.md](../AGENTS.md).
