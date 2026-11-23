Status: current

# Releases

No release has been published. `internal/version` reports `0.0.0-dev`.

When releases start, they follow semantic versioning:

- A patch release fixes a bug without changing the API.
- A minor release adds behavior that existing callers can ignore.
- A major release changes or removes behavior that callers rely on.

A release is a tag, `vMAJOR.MINOR.PATCH`, plus an entry in [CHANGELOG.md](../../CHANGELOG.md). CI builds `inbai` and `inbaid` for macOS, Linux, and Windows and publishes the binaries with checksums. A maintainer creates the tag. The version constant is updated in the same change that prepares the release.

Pre-alpha tags may exist before the API is stable. They are marked as such in the changelog, and callers should not depend on them.
