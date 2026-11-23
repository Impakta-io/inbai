// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSnapshotReportPrintsJSON(t *testing.T) {
	var stdout bytes.Buffer
	reportSnapshot(&stdout, true, "backup", "inbai.json", 2, 1)
	if !strings.Contains(stdout.String(), `"events":2`) || strings.Contains(stdout.String(), "message bodies") {
		t.Fatalf("json %s", stdout.String())
	}
	stdout.Reset()
	reportSnapshot(&stdout, false, "restore", "inbai.json", 2, 1)
	if !strings.Contains(stdout.String(), "restored 2 events and 1 suppressed recipients") {
		t.Fatalf("text %s", stdout.String())
	}
	var stderr bytes.Buffer
	if code := run([]string{"backup", "--json"}, &stdout, &stderr, func(string) string { return "" }); code != 2 {
		t.Fatalf("code %d %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--json") {
		t.Fatalf("usage %s", stderr.String())
	}
}
