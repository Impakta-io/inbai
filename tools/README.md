Status: current

# Tools

`docscheck` checks public Markdown. It fails when a file has no `Status:` line, a relative link points nowhere, or a public file mentions the gitignored planning directory.

```sh
go run ./tools/docscheck
```

`make docs-check` runs the same command.
