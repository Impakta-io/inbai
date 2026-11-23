// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestSlug(t *testing.T) {
	tests := map[string]string{
		"Build and test":               "build-and-test",
		"OAuth 2.1 client credentials": "oauth-21-client-credentials",
		"Don't skip the check":         "dont-skip-the-check",
	}
	for title, want := range tests {
		if got := slug(title); got != want {
			t.Errorf("slug(%q) = %q, want %q", title, got, want)
		}
	}
}
