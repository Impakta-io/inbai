Status: current

# Releases

Development builds report `0.0.0-dev`. Published pre-alpha releases are listed in [CHANGELOG.md](../../CHANGELOG.md).

Releases follow semantic versioning:

- A patch release fixes a bug without changing the API.
- A minor release adds behavior that existing callers can ignore.
- A major release changes or removes behavior that callers rely on.

A release is a tag, `vMAJOR.MINOR.PATCH`, plus an entry in [CHANGELOG.md](../../CHANGELOG.md). Pushing a `v*` tag runs `.github/workflows/release.yml`, which runs `go test ./...` and the reference and docs checks, then builds `inbai` and `inbaid` for macOS (`arm64` and `amd64`), Linux (`amd64` and `arm64`), and Windows (`amd64`) with `-ldflags "-X github.com/Impakta-io/inbai/internal/version.Version=<version>"`, publishes the archives, `checksums.txt`, and rendered `inbai.rb` formula on a GitHub Release, and marks tags containing `-` as prereleases. A maintainer creates the tag.

## Homebrew tap

When the `HOMEBREW_TAP_TOKEN` repository secret is configured with write access to `Impakta-io/homebrew-tap`, the release workflow commits the rendered `Formula/inbai.rb` to that tap automatically. To set up the tap once:

1. Create a public GitHub repository named `Impakta-io/homebrew-tap`.
2. Add a repository secret `HOMEBREW_TAP_TOKEN` (fine-grained PAT with `Contents: Read and write` on `Impakta-io/homebrew-tap`) to `Impakta-io/inbai`.
3. Push a release tag (for example `v0.1.0`). Users can then install both binaries with `brew install impakta-io/tap/inbai`.

`packaging/homebrew/render.sh` renders `Formula/inbai.rb` from a version and `checksums.txt`.

Pre-alpha tags may exist before the API is stable. They are marked as such in the changelog, and callers should not depend on them.
