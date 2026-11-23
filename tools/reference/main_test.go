// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

func TestOperationsKeepMethodAndPath(t *testing.T) {
	text := "" +
		"paths:\n" +
		"  /v1/inboxes:\n" +
		"    post:\n" +
		"      operationId: createInbox\n" +
		"      summary: Create one inbox.\n" +
		"    get:\n" +
		"      operationId: listInboxes\n" +
		"      summary: List accounts.\n"
	ops, err := operations(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 || ops[0].id != "createInbox" || ops[0].method != "post" || ops[1].method != "get" || ops[1].path != "/v1/inboxes" {
		t.Fatalf("%+v", ops)
	}
}

func TestHTTPReferenceListsEveryOperation(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	page, err := httpReference(root + "/api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"createAgent", "suspendAgent", "revokeGrant", "downloadAttachment", "listEvents"} {
		if !strings.Contains(page, "`"+id+"`") {
			t.Fatalf("missing %s\n%s", id, page)
		}
	}
}

func TestGeneratedPagesMatchTheTree(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := write(root, true); err != nil {
		t.Fatal(err)
	}
}
func TestCLIReferenceListsAgentCommands(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	page, err := cliReference(root + "/cmd/inbai")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"usage: inbai agent grant <agent> <inbox> <permission>",
		"usage: inbai agent revoke <agent> <inbox> <permission>",
		"usage: inbai backup --file path [--json]",
		"usage: inbai restore --file path [--json]",
		"usage: inbai init --domain name",
	} {
		if !strings.Contains(page, line) {
			t.Fatalf("missing %s\n%s", line, page)
		}
	}
}
