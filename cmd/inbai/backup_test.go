// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestBackupRequiresTheDatabaseURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"backup", "--file", "inbai.json"}, &stdout, &stderr, func(string) string { return "" })
	if code != 2 || !strings.Contains(stderr.String(), "INBAI_DATABASE_URL") {
		t.Fatalf("code %d %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"restore", "--file", "missing.json"}, &stdout, &stderr, func(string) string { return "" })
	if code != 2 || !strings.Contains(stderr.String(), "INBAI_DATABASE_URL") {
		t.Fatalf("restore %d %s", code, stderr.String())
	}
}
