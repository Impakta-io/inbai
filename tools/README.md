Status: current

# Tools

`docscheck` checks public Markdown. It fails when a file has no `Status:` line, a relative link points nowhere, or a public file mentions the gitignored planning directory. `reference` writes the CLI and HTTP reference pages and, with `-check`, fails when a generated page is stale.

```sh
go run ./tools/reference -check
go run ./tools/docscheck
```

`make docs-check` runs both commands. `go run ./tools/reference` rewrites the pages.
