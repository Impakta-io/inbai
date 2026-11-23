// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/version"
)

func TestRunPrintsVersionWithoutArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, func(string) string { return "" }); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if stdout.String() != version.String(version.Server)+"\n" {
		t.Fatalf("stdout %q", stdout.String())
	}
}

func TestServeRequiresOperatorToken(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"serve"}, &stdout, &stderr, func(string) string { return "" })
	if code != 2 || !strings.Contains(stderr.String(), "INBAI_OPERATOR_TOKEN") {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
}
