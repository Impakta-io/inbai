#!/usr/bin/env bash
# Copyright 2026 Impakta-io
# SPDX-License-Identifier: Apache-2.0
#
# Renders a Homebrew Formula/inbai.rb for a release tag from checksums.txt.
# Usage: packaging/homebrew/render.sh <version-without-v> <checksums.txt>

set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <version> <checksums.txt>" >&2
  exit 2
fi

VERSION="${1#v}"
CHECKSUMS="$2"
REPO="${INBAI_REPO:-Impakta-io/inbai}"
BASE_URL="https://github.com/${REPO}/releases/download/v${VERSION}"

sha_for() {
  local file="inbai_${VERSION}_${1}.tar.gz"
  local sha
  sha=$(awk -v f="$file" '$2 == f || $2 == ("./" f) { print $1; exit }' "$CHECKSUMS")
  if [ -z "$sha" ]; then
    echo "missing checksum for ${file} in ${CHECKSUMS}" >&2
    exit 1
  fi
  printf '%s' "$sha"
}

DARWIN_ARM64_SHA=$(sha_for darwin_arm64)
DARWIN_AMD64_SHA=$(sha_for darwin_amd64)
LINUX_ARM64_SHA=$(sha_for linux_arm64)
LINUX_AMD64_SHA=$(sha_for linux_amd64)

cat <<EOF
# typed: false
# frozen_string_literal: true

class Inbai < Formula
  desc "Agent-native email control plane CLI and server"
  homepage "https://github.com/${REPO}"
  version "${VERSION}"
  license "Apache-2.0"

  on_macos do
    if Hardware::CPU.arm?
      url "${BASE_URL}/inbai_${VERSION}_darwin_arm64.tar.gz"
      sha256 "${DARWIN_ARM64_SHA}"
    else
      url "${BASE_URL}/inbai_${VERSION}_darwin_amd64.tar.gz"
      sha256 "${DARWIN_AMD64_SHA}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "${BASE_URL}/inbai_${VERSION}_linux_arm64.tar.gz"
      sha256 "${LINUX_ARM64_SHA}"
    else
      url "${BASE_URL}/inbai_${VERSION}_linux_amd64.tar.gz"
      sha256 "${LINUX_AMD64_SHA}"
    end
  end

  def install
    bin.install "inbai"
    bin.install "inbaid"
  end

  test do
    assert_match "inbai #{version}", shell_output("#{bin}/inbai version")
    assert_match "inbaid #{version}", shell_output("#{bin}/inbaid version")
  end
end
EOF
