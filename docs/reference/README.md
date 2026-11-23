Status: planned — a generated CLI reference and a generated HTTP reference are not built. The operations themselves are in the CLI and in `api/openapi.yaml`.

# Reference

This directory will hold generated reference pages.

## Planned sections

- CLI reference, generated from the `inbai` command tree.
- HTTP reference, generated from [api/openapi.yaml](../../api/openapi.yaml).

The CLI implements `init`, `domain add`, `doctor`, inbox create and generate, message send, list, get, reply, and delete, `mcp serve`, `backup`, and `restore`. Inbox and message commands call `inbaid`. Generated reference pages are not built yet.
