// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package version

import "testing"

func TestString(t *testing.T) {
	if got := String(CLI); got != "inbai 0.0.0-dev" {
		t.Fatalf("String(CLI) = %q", got)
	}
	if got := String(Server); got != "inbaid 0.0.0-dev" {
		t.Fatalf("String(Server) = %q", got)
	}
}

func TestVersionOverride(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = "0.1.0"
	if got := String(CLI); got != "inbai 0.1.0" {
		t.Fatalf("String(CLI) = %q", got)
	}
}
