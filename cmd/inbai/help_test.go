// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/version"
)

func noEnvironment(string) string { return "" }

func runCapture(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, noEnvironment)
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
	for _, part := range []string{
		"Operator CLI for inbAI, self-hosted email for AI agents.",
		"Usage: inbai <command> [flags]",
		"Configuration:",
		"INBAI_OPERATOR_TOKEN",
		"INBAI_CLIENT_ID with INBAI_CLIENT_SECRET",
		"Run 'inbai help <command>' for details.",
		"https://github.com/Impakta-io/inbai/tree/main/docs",
	} {
		if !strings.Contains(want, part) {
			t.Fatalf("top help has no %q\n%s", part, want)
		}
	}
	listed := map[string]bool{}
	for _, section := range program.Sections {
		for _, name := range section.Names {
			if _, ok := program.Lookup(name); !ok {
				t.Fatalf("section %s lists %q, which is not a command", section.Title, name)
			}
			if !strings.Contains(want, "\n  "+name+" ") {
				t.Fatalf("top help does not show %q", name)
			}
			listed[name] = true
		}
	}
	// Every command is listed, or sits under a group that is.
	for _, c := range program.All() {
		if c.Run == nil {
			continue
		}
		words := strings.Fields(c.Name)
		found := false
		for i := 1; i <= len(words); i++ {
			found = found || listed[strings.Join(words[:i], " ")]
		}
		if !found {
			t.Fatalf("top help does not reach %q", c.Name)
		}
	}
	lines := strings.Split(strings.TrimSuffix(want, "\n"), "\n")
	if len(lines) > 50 {
		t.Fatalf("top help has %d lines", len(lines))
	}
	for _, line := range lines {
		if len(line) > 80 {
			t.Fatalf("line longer than 80 columns: %q", line)
		}
	}
}

func TestEveryCommandHasHelp(t *testing.T) {
	for _, c := range program.All() {
		t.Run(c.Name, func(t *testing.T) {
			words := strings.Fields(c.Name)
			if c.Summary == "" || strings.HasSuffix(c.Summary, ".") {
				t.Fatalf("summary %q", c.Summary)
			}
			if c.Run != nil && len(c.Usage) == 0 {
				t.Fatalf("no usage line")
			}
			for _, line := range c.Usage {
				if line != "inbai "+c.Name && !strings.HasPrefix(line, "inbai "+c.Name+" ") {
					t.Fatalf("usage %q does not start with the command", line)
				}
			}
			if c.Run != nil && len(c.Examples) == 0 {
				t.Fatalf("no example")
			}
			code, help, stderr := runCapture(append([]string{"help"}, words...)...)
			if code != 0 || stderr != "" {
				t.Fatalf("help exit %d stderr %q", code, stderr)
			}
			if !strings.Contains(help, c.Summary+".") {
				t.Fatalf("help has no summary\n%s", help)
			}
			for _, line := range c.Usage {
				if !strings.Contains(help, "  "+line+"\n") {
					t.Fatalf("help has no usage %q\n%s", line, help)
				}
			}
			for _, flag := range []string{"--help", "-h", "-help"} {
				code, stdout, stderr := runCapture(append(words, flag)...)
				if code != 0 || stderr != "" || stdout != help {
					t.Fatalf("%s exit %d stdout %q stderr %q", flag, code, stdout, stderr)
				}
			}
			if c.Run == nil {
				code, stdout, stderr := runCapture(words...)
				if code != 2 || stdout != "" || stderr != help {
					t.Fatalf("group alone exit %d stdout %q stderr %q", code, stdout, stderr)
				}
				if !strings.Contains(help, "Commands:\n") {
					t.Fatalf("group help lists no subcommands\n%s", help)
				}
				return
			}
			if c.NoFlags {
				return
			}
			flags := program.Flags(&c)
			if flags == "" || !strings.Contains(help, "Flags:\n"+flags) {
				t.Fatalf("help has no flags %q\n%s", flags, help)
			}
			if c.Positional != strings.Contains(help, "Flags go before arguments.") {
				t.Fatalf("positional note: %v\n%s", c.Positional, help)
			}
		})
	}
}

func TestUnknownCommandSuggestsOne(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		name    string
		suggest string
		helpFor string
	}{
		{args: []string{"mesage"}, name: "mesage", suggest: "message", helpFor: "inbai help"},
		{args: []string{"inbx", "list"}, name: "inbx", suggest: "inbox", helpFor: "inbai help"},
		{args: []string{"nope"}, name: "nope", helpFor: "inbai help"},
		{args: []string{"--nope"}, name: "--nope", helpFor: "inbai help"},
		{args: []string{"message", "sned"}, name: "message sned", suggest: "message send", helpFor: "inbai help message"},
		{args: []string{"inbox", "allowlist", "gte"}, name: "inbox allowlist gte", suggest: "inbox allowlist get", helpFor: "inbai help inbox allowlist"},
		{args: []string{"help", "agnet"}, name: "agnet", suggest: "agent", helpFor: "inbai help"},
		{args: []string{"help", "retention", "nope"}, name: "retention nope", helpFor: "inbai help retention"},
	} {
		code, stdout, stderr := runCapture(tc.args...)
		if code != 2 || stdout != "" {
			t.Fatalf("args %v exit %d stdout %q", tc.args, code, stdout)
		}
		want := "inbai: unknown command \"" + tc.name + "\"\n"
		if tc.suggest != "" {
			want += "Did you mean \"" + tc.suggest + "\"?\n"
		}
		want += "Run '" + tc.helpFor + "' for usage.\n"
		if stderr != want {
			t.Fatalf("args %v stderr %q, want %q", tc.args, stderr, want)
		}
	}
}

func TestHelpFlagAfterAValueIsLeftToTheCommand(t *testing.T) {
	// Go's flag package reads "-h" here as the value of --body, so the
	// command runs and reports its missing flags, as it did before.
	code, stdout, stderr := runCapture("message", "send", "--body", "-h")
	if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "usage: inbai message send ") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestBadFlagStillExitsTwoWithUsage(t *testing.T) {
	code, stdout, stderr := runCapture("inbox", "list", "--nope")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "flag provided but not defined: -nope") || !strings.Contains(stderr, "usage: inbai inbox list") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestVersionPrintsTheVersion(t *testing.T) {
	code, stdout, stderr := runCapture("version")
	if code != 0 || stderr != "" || stdout != version.String(version.CLI)+"\n" {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestHelpJSONIsTheTable(t *testing.T) {
	code, stdout, stderr := runCapture("help", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	var got struct {
		Program  string `json:"program"`
		Commands []struct {
			Name  string   `json:"name"`
			Usage []string `json:"usage"`
			Flags string   `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	all := program.All()
	if got.Program != "inbai" || len(got.Commands) != len(all) {
		t.Fatalf("program %q with %d commands, table has %d", got.Program, len(got.Commands), len(all))
	}
	for i, c := range all {
		if got.Commands[i].Name != c.Name || strings.Join(got.Commands[i].Usage, "\n") != strings.Join(c.Usage, "\n") || got.Commands[i].Flags != program.Flags(&c) {
			t.Fatalf("command %d is %+v, table has %s", i, got.Commands[i], c.Name)
		}
	}
}

// The usage lines commands print on a bad call must be the ones help shows.
func TestPrintedUsageLinesAreInTheTable(t *testing.T) {
	inTable := map[string]bool{}
	for _, c := range program.All() {
		for _, line := range c.Usage {
			inTable[line] = true
		}
	}
	usageRE := regexp.MustCompile(`"usage: (inbai [^"]*?)(\\n)?"`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range usageRE.FindAllStringSubmatch(string(raw), -1) {
			found++
			if !inTable[match[1]] {
				t.Errorf("%s prints %q, which is not a usage line in commands.go", name, match[1])
			}
		}
	}
	if found == 0 {
		t.Fatal("found no usage lines")
	}
}
