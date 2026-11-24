// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

func runCapture(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, func(string) string { return "" })
	return code, stdout.String(), stderr.String()
}

func TestTopHelpListsEveryCommand(t *testing.T) {
	_, want, _ := runCapture()
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
		code, stdout, stderr := runCapture(args...)
		if code != 0 || stderr != "" || stdout != want {
			t.Fatalf("args %v exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
	for _, c := range program.All() {
		if !strings.Contains(want, "\n  "+c.Name+" ") {
			t.Fatalf("top help does not show %q\n%s", c.Name, want)
		}
	}
	if !strings.Contains(want, "Run 'inbaid help <command>' for details.") {
		t.Fatalf("top help\n%s", want)
	}
}

func TestEveryCommandHasHelp(t *testing.T) {
	for _, c := range program.All() {
		t.Run(c.Name, func(t *testing.T) {
			if c.Summary == "" || len(c.Usage) == 0 || len(c.Examples) == 0 {
				t.Fatalf("summary %q usage %v examples %v", c.Summary, c.Usage, c.Examples)
			}
			code, help, stderr := runCapture("help", c.Name)
			if code != 0 || stderr != "" || !strings.Contains(help, c.Summary+".") || !strings.Contains(help, c.Usage[0]) {
				t.Fatalf("help exit %d stdout %q stderr %q", code, help, stderr)
			}
			for _, flag := range []string{"--help", "-h"} {
				code, stdout, stderr := runCapture(c.Name, flag)
				if code != 0 || stderr != "" || stdout != help {
					t.Fatalf("%s exit %d stdout %q stderr %q", flag, code, stdout, stderr)
				}
			}
		})
	}
}

func TestServeHelpDocumentsFlagsAndEnvironment(t *testing.T) {
	code, help, _ := runCapture("serve", "--help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, flag := range []string{"-addr string", "-config string", `(default "127.0.0.1:8787")`} {
		if !strings.Contains(help, flag) {
			t.Fatalf("serve help has no %q\n%s", flag, help)
		}
	}
	for _, group := range []string{"Core:", "Database:", "Mail mode:", "IMAP mode:", "Limits and quotas:", "Webhooks:", "Retention:"} {
		if !strings.Contains(help, "\n  "+group+"\n") {
			t.Fatalf("serve help has no group %q\n%s", group, help)
		}
	}
	// Every INBAI_ variable the server code names is documented.
	nameRE := regexp.MustCompile(`"(INBAI_[A-Z_]+)"`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range nameRE.FindAllStringSubmatch(string(raw), -1) {
			if !strings.Contains(help, "\n    "+match[1]+" ") {
				t.Errorf("%s reads %s, which serve --help does not list", name, match[1])
			}
		}
	}
}

func TestUnknownCommandSuggestsOne(t *testing.T) {
	code, stdout, stderr := runCapture("srve")
	want := "inbaid: unknown command \"srve\"\nDid you mean \"serve\"?\nRun 'inbaid help' for usage.\n"
	if code != 2 || stdout != "" || stderr != want {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestServeBadFlagExitsTwo(t *testing.T) {
	code, stdout, stderr := runCapture("serve", "--nope")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "flag provided but not defined: -nope") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}
