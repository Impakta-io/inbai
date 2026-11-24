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
	crlfOps, err := operations(strings.ReplaceAll(text, "\n", "\r\n"))
	if err != nil || len(crlfOps) != 2 {
		t.Fatalf("crlf ops=%+v err=%v", crlfOps, err)
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
func TestCLIReferenceRendersEachPart(t *testing.T) {
	page := cliReference([]cliTable{{
		Program: "inbai",
		Summary: "Operator CLI.",
		Commands: []cliCommand{
			{Name: "inbox", Summary: "Manage inboxes", Group: true},
			{
				Name:        "inbox create",
				Summary:     "Create one inbox address",
				Usage:       []string{"inbai inbox create <local-part>"},
				Description: "The address is <local-part>@<domain>.\n\nGrant '*' later.",
				Flags:       "  -json\n    \tprint JSON\n",
				Positional:  true,
				Env:         []cliEnv{{Group: "Core", Name: "INBAI_X", Text: "a | b", Default: "1"}},
				Examples:    []string{"inbai inbox create alice"},
			},
		},
	}})
	for _, want := range []string{
		"Status: current\n",
		"## inbai\n\nOperator CLI.\n",
		"| [`inbai inbox create`](#inbai-inbox-create) | Create one inbox address |\n",
		"### inbai inbox\n\nManage inboxes.\n\nRun `inbai help inbox <command>` for one of its subcommands.\n",
		"### inbai inbox create\n\nCreate one inbox address.\n\n```text\ninbai inbox create <local-part>\n```\n",
		"The address is &lt;local-part&gt;@&lt;domain&gt;.\n\nGrant '\\*' later.\n",
		"Flags:\n\n```text\n  -json\n    \tprint JSON\n```\n\nFlags go before arguments.",
		"| `INBAI_X` | Core: a \\| b | 1 |\n",
		"Examples:\n\n```sh\ninbai inbox create alice\n```\n",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %q\n%s", want, page)
		}
	}
	if strings.HasSuffix(page, "\n\n") {
		t.Fatalf("page ends with a blank line")
	}
}

func TestCLIReferenceListsEveryCommand(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	var tables []cliTable
	for _, binary := range []string{"inbai", "inbaid"} {
		table, err := loadTable(root, binary)
		if err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	page := cliReference(tables)
	for _, table := range tables {
		for _, c := range table.Commands {
			if !strings.Contains(page, "\n### "+table.Program+" "+c.Name+"\n") {
				t.Fatalf("missing %s %s", table.Program, c.Name)
			}
			for _, line := range c.Usage {
				if !strings.Contains(page, line+"\n") {
					t.Fatalf("missing usage %s", line)
				}
			}
		}
	}
	for _, want := range []string{
		"inbai agent grant <agent> <inbox> <permission>",
		"inbai backup --file path [--json]",
		"inbai init --domain name",
		"-quota-bytes int",
		"`INBAI_WEBHOOK_WORKERS`",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %s", want)
		}
	}
}
