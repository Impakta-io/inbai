// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"flag"
	"io"
	"strings"
	"testing"
)

// testProgram has one command with a string flag, a bool flag, and a
// positional argument. Run records the arguments it was given.
func testProgram(got *[]string) *Program {
	send := func(args []string, _, stderr io.Writer, _ func(string) string) int {
		fs := flag.NewFlagSet("send", flag.ContinueOnError)
		fs.SetOutput(stderr)
		fs.String("body", "", "message body")
		fs.Bool("json", false, "print JSON")
		if err := fs.Parse(args); err != nil {
			return 2
		}
		*got = append([]string{"ran"}, fs.Args()...)
		return 0
	}
	return &Program{
		Name:    "tool",
		Summary: "A tool.",
		Commands: []Command{
			{Name: "mail", Summary: "Mail things"},
			{Name: "mail send", Summary: "Send mail", Usage: []string{"tool mail send [--body text] <to>"}, Positional: true, Examples: []string{"tool mail send bob"}, Run: send},
		},
		Sections: []Section{{Title: "Mail", Names: []string{"mail send", "help"}}},
	}
}

func TestHelpIsWhatTheFlagParserWouldReport(t *testing.T) {
	for _, tc := range []struct {
		args []string
		help bool
	}{
		{args: []string{"-h"}, help: true},
		{args: []string{"--help"}, help: true},
		{args: []string{"-json", "-h"}, help: true},
		{args: []string{"--body=x", "--help"}, help: true},
		{args: []string{"--body", "-h"}},
		{args: []string{"bob", "-h"}},
		{args: []string{"--", "-h"}},
		{args: []string{"bob"}},
	} {
		var got []string
		p := testProgram(&got)
		var stdout, stderr bytes.Buffer
		code := p.Main(append([]string{"mail", "send"}, tc.args...), &stdout, &stderr, nil)
		isHelp := strings.HasPrefix(stdout.String(), "Usage:\n  tool mail send")
		if code != 0 || isHelp != tc.help || (got != nil) == tc.help {
			t.Fatalf("args %v exit %d help %v ran %v stdout %q stderr %q", tc.args, code, isHelp, got, stdout.String(), stderr.String())
		}
	}
}

func TestHelpShowsTheCommandFlags(t *testing.T) {
	var got []string
	p := testProgram(&got)
	var stdout bytes.Buffer
	p.WriteHelp(&stdout, &p.Commands[1])
	want := "Usage:\n  tool mail send [--body text] <to>\n\nSend mail.\n\n" +
		"Flags:\n  -body string\n    \tmessage body\n  -json\n    \tprint JSON\n\n" +
		"Flags go before arguments. Flag parsing stops at the first argument.\n\n" +
		"Examples:\n  tool mail send bob\n"
	if stdout.String() != want {
		t.Fatalf("help\n%s\nwant\n%s", stdout.String(), want)
	}
	if got != nil {
		t.Fatalf("writing help ran the command: %v", got)
	}
}

func TestGroupWithoutSubcommand(t *testing.T) {
	var got []string
	p := testProgram(&got)
	var stdout, stderr bytes.Buffer
	if code := p.Main([]string{"mail"}, &stdout, &stderr, nil); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "  mail send  Send mail\n") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	group := stderr.String()
	stdout.Reset()
	stderr.Reset()
	if code := p.Main([]string{"mail", "--help"}, &stdout, &stderr, nil); code != 0 || stdout.String() != group || stderr.Len() != 0 {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"message", "message", 0},
		{"mesage", "message", 1},
		{"sned", "send", 1},
		{"inbx", "inbox", 1},
		{"nope", "init", 4},
		{"", "abc", 3},
	} {
		if got := distance(tc.a, tc.b); got != tc.want {
			t.Fatalf("distance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSuggest(t *testing.T) {
	candidates := []string{"init", "inbox", "message", "help"}
	for _, tc := range []struct{ name, want string }{
		{"mesage", "message"},
		{"inbx", "inbox"},
		{"hlep", "help"},
		{"nope", ""},
		{"x", ""},
	} {
		if got := suggest(tc.name, candidates); got != tc.want {
			t.Fatalf("suggest(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := suggest("inbox lsit", []string{"inbox list", "inbox delete"}); got != "inbox list" {
		t.Fatalf("subcommand suggestion %q", got)
	}
}

func TestWrap(t *testing.T) {
	got := wrap("one two three four", 9)
	if got != "one two\nthree\nfour" {
		t.Fatalf("wrap %q", got)
	}
}
