// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

// Package cli dispatches the inbai and inbaid commands and prints their help.
// Each binary describes its commands in one table. Dispatch, help, and the
// generated CLI reference all read that table.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// RunFunc runs one command. args are the arguments after the command name.
type RunFunc func(args []string, stdout, stderr io.Writer, getenv func(string) string) int

// Command is one row of a command table.
type Command struct {
	// Name is the command path without the program, such as "inbox create".
	Name string
	// Summary is one line, without a final period.
	Summary string
	// Usage lines start with the program name. A group may leave it empty.
	Usage []string
	// Description is optional. Paragraphs are separated by a blank line.
	Description string
	// Env lists environment variables the command reads.
	Env []EnvVar
	// Examples are full command lines.
	Examples []string
	// Positional is true when the command takes arguments after its flags.
	Positional bool
	// NoFlags is true when Run defines no flag set.
	NoFlags bool
	// Run is nil for a group, which only holds subcommands.
	Run RunFunc
}

// EnvVar is one environment variable in a command's help.
type EnvVar struct {
	Group   string `json:"group,omitempty"`
	Name    string `json:"name"`
	Text    string `json:"text"`
	Default string `json:"default,omitempty"`
}

// Section is one heading of the top-level help.
type Section struct {
	Title string
	Names []string
}

// Program is a binary and its command table.
type Program struct {
	Name     string
	Summary  string
	Commands []Command
	Sections []Section
	// Footer is printed verbatim after the sections.
	Footer  string
	DocsURL string
}

// Main dispatches args and returns the exit code.
func (p *Program) Main(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 || isHelpFlag(args[0]) {
		p.WriteTop(stdout)
		return 0
	}
	c, rest, ok := p.resolve(args, stderr)
	if !ok {
		return 2
	}
	if c.Run == nil {
		if len(rest) == 0 {
			p.WriteHelp(stderr, c)
			return 2
		}
		if isHelpFlag(rest[0]) {
			p.WriteHelp(stdout, c)
			return 0
		}
		p.unknown(stderr, c.Name+" "+rest[0], c)
		return 2
	}
	if p.wantsHelp(c, rest) {
		p.WriteHelp(stdout, c)
		return 0
	}
	return c.Run(rest, stdout, stderr, getenv)
}

// Lookup returns the command with that name path.
func (p *Program) Lookup(name string) (*Command, bool) {
	if name == "help" {
		c := p.helpCommand()
		return &c, true
	}
	for i := range p.Commands {
		if p.Commands[i].Name == name {
			return &p.Commands[i], true
		}
	}
	return nil, false
}

// All returns the table with the built-in help command at the end.
func (p *Program) All() []Command {
	return append(append([]Command(nil), p.Commands...), p.helpCommand())
}

func (p *Program) helpCommand() Command {
	return Command{
		Name:    "help",
		Summary: "Show help for a command",
		Usage:   []string{p.Name + " help [<command> [<subcommand>]]"},
		Description: "With no command, help lists every command. " + p.Name + " <command> --help and -h print the same text as " +
			p.Name + " help <command>. " + p.Name + " help --json prints the command table as JSON for tools.",
		Examples:   []string{p.Name + " help", p.Name + " help " + p.Commands[0].Name},
		Positional: true,
		NoFlags:    true,
		Run: func(args []string, stdout, stderr io.Writer, _ func(string) string) int {
			return p.help(args, stdout, stderr)
		},
	}
}

// resolve finds the longest command path at the front of args. It reports an
// unknown first word on stderr.
func (p *Program) resolve(args []string, stderr io.Writer) (*Command, []string, bool) {
	c, ok := p.Lookup(args[0])
	if !ok || strings.HasPrefix(args[0], "-") {
		p.unknown(stderr, args[0], nil)
		return nil, nil, false
	}
	i := 1
	for i < len(args) {
		next, ok := p.Lookup(c.Name + " " + args[i])
		if !ok {
			break
		}
		c = next
		i++
	}
	return c, args[i:], true
}

func (p *Program) help(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--json" {
		if err := p.WriteJSON(stdout); err != nil {
			writef(stderr, "%s: %s\n", p.Name, err)
			return 1
		}
		return 0
	}
	var words []string
	for _, arg := range args {
		if !isHelpFlag(arg) {
			words = append(words, arg)
		}
	}
	if len(words) == 0 {
		if len(args) > 0 {
			c := p.helpCommand()
			p.WriteHelp(stdout, &c)
			return 0
		}
		p.WriteTop(stdout)
		return 0
	}
	c, rest, ok := p.resolve(words, stderr)
	if !ok {
		return 2
	}
	if len(rest) > 0 {
		p.unknown(stderr, c.Name+" "+rest[0], c)
		return 2
	}
	p.WriteHelp(stdout, c)
	return 0
}

// unknown reports a command that is not in the table. parent is the command
// whose subcommands are the candidates, or nil for the top level.
func (p *Program) unknown(w io.Writer, name string, parent *Command) {
	writef(w, "%s: unknown command %q\n", p.Name, name)
	var candidates []string
	for _, c := range p.All() {
		if parent == nil && !strings.Contains(c.Name, " ") {
			candidates = append(candidates, c.Name)
		}
		if parent != nil && p.isChild(parent, &c) {
			candidates = append(candidates, c.Name)
		}
	}
	if best := suggest(name, candidates); best != "" {
		writef(w, "Did you mean %q?\n", best)
	}
	if parent == nil {
		writef(w, "Run '%s help' for usage.\n", p.Name)
		return
	}
	writef(w, "Run '%s help %s' for usage.\n", p.Name, parent.Name)
}

func (p *Program) isChild(parent, c *Command) bool {
	rest, ok := strings.CutPrefix(c.Name, parent.Name+" ")
	return ok && !strings.Contains(rest, " ")
}

// Descendants returns every runnable command under c, in table order.
func (p *Program) Descendants(c *Command) []Command {
	var out []Command
	for _, sub := range p.Commands {
		if strings.HasPrefix(sub.Name, c.Name+" ") && sub.Run != nil {
			out = append(out, sub)
		}
	}
	return out
}

// wantsHelp reports whether the command's own flag parsing would stop with
// flag.ErrHelp. It parses args with a copy of the command's flags, so a flag
// value of "-h" or a "-h" after the first argument is left to the command.
func (p *Program) wantsHelp(c *Command, args []string) bool {
	if c.NoFlags {
		return len(args) > 0 && isHelpFlag(args[0])
	}
	fs := flag.NewFlagSet(c.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for _, f := range parseDefaults(p.Flags(c)) {
		if f.bool {
			fs.Bool(f.name, false, "")
		} else {
			fs.String(f.name, "", "")
		}
	}
	return errors.Is(fs.Parse(args), flag.ErrHelp)
}

// Flags returns the command's flags as flag.PrintDefaults writes them. It asks
// the command's own flag set by running the command with -h, which every
// command parses before it does anything else.
func (p *Program) Flags(c *Command) string {
	if c.Run == nil || c.NoFlags {
		return ""
	}
	var buf bytes.Buffer
	c.Run([]string{"-h"}, io.Discard, &buf, func(string) string { return "" })
	out := buf.String()
	if strings.HasPrefix(out, "  -") {
		return out
	}
	if i := strings.Index(out, "\n  -"); i >= 0 {
		return out[i+1:]
	}
	return ""
}

type flagInfo struct {
	name string
	bool bool
}

// parseDefaults reads the flag names from flag.PrintDefaults output. A flag
// line with no type name is a boolean flag.
func parseDefaults(text string) []flagInfo {
	var out []flagInfo
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "  -") {
			continue
		}
		head, _, _ := strings.Cut(line, "\t")
		fields := strings.Fields(head)
		out = append(out, flagInfo{name: strings.TrimPrefix(fields[0], "-"), bool: len(fields) == 1})
	}
	return out
}

func isHelpFlag(arg string) bool {
	switch arg {
	case "-h", "-help", "--h", "--help":
		return true
	}
	return false
}

// WriteTop writes the top-level help.
func (p *Program) WriteTop(w io.Writer) {
	writef(w, "%s\n\n", p.Summary)
	writef(w, "Usage: %s <command> [flags]\n\n", p.Name)
	width := 0
	for _, s := range p.Sections {
		for _, name := range s.Names {
			width = max(width, len(name))
		}
	}
	for _, s := range p.Sections {
		writef(w, "%s:\n", s.Title)
		for _, name := range s.Names {
			summary := ""
			if c, ok := p.Lookup(name); ok {
				summary = c.Summary
			}
			writef(w, "  %-*s  %s\n", width, name, summary)
		}
	}
	if p.Footer != "" {
		writef(w, "\n%s", p.Footer)
	}
	writef(w, "\nRun '%s help <command>' for details.\n", p.Name)
	if p.DocsURL != "" {
		writef(w, "Docs: %s\n", p.DocsURL)
	}
}

// WriteHelp writes the help for one command or group.
func (p *Program) WriteHelp(w io.Writer, c *Command) {
	usage := c.Usage
	if len(usage) == 0 && c.Run == nil {
		usage = []string{p.Name + " " + c.Name + " <command> [flags]"}
	}
	writef(w, "Usage:\n")
	for _, line := range usage {
		writef(w, "  %s\n", line)
	}
	writef(w, "\n%s.\n", c.Summary)
	for _, para := range paragraphs(c.Description) {
		writef(w, "\n%s\n", wrap(para, 78))
	}
	if subs := p.Descendants(c); len(subs) > 0 {
		writef(w, "\nCommands:\n")
		width := 0
		for _, sub := range subs {
			width = max(width, len(sub.Name))
		}
		for _, sub := range subs {
			writef(w, "  %-*s  %s\n", width, sub.Name, sub.Summary)
		}
	}
	if flags := p.Flags(c); flags != "" {
		writef(w, "\nFlags:\n%s", flags)
		if c.Positional {
			writef(w, "\nFlags go before arguments. Flag parsing stops at the first argument.\n")
		}
	}
	if len(c.Env) > 0 {
		writef(w, "\nEnvironment:\n")
		writeEnv(w, c.Env)
	}
	if len(c.Examples) > 0 {
		writef(w, "\nExamples:\n")
		for _, ex := range c.Examples {
			writef(w, "  %s\n", ex)
		}
	}
	if c.Run == nil {
		writef(w, "\nRun '%s help %s <command>' for details.\n", p.Name, c.Name)
	}
}

// writeEnv writes one line per variable, aligned within each group.
func writeEnv(w io.Writer, vars []EnvVar) {
	for start := 0; start < len(vars); {
		end := start
		width := 0
		for end < len(vars) && vars[end].Group == vars[start].Group {
			width = max(width, len(vars[end].Name))
			end++
		}
		indent := "  "
		if vars[start].Group != "" {
			writef(w, "  %s:\n", vars[start].Group)
			indent = "    "
		}
		for _, v := range vars[start:end] {
			text := v.Text
			if v.Default != "" {
				text += " (default " + v.Default + ")"
			}
			writef(w, "%s%-*s  %s\n", indent, width, v.Name, text)
		}
		start = end
	}
}

// Reference is the table as WriteJSON writes it.
type Reference struct {
	Program  string             `json:"program"`
	Summary  string             `json:"summary"`
	Commands []ReferenceCommand `json:"commands"`
}

// ReferenceCommand is one command in a Reference.
type ReferenceCommand struct {
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Group       bool     `json:"group,omitempty"`
	Usage       []string `json:"usage,omitempty"`
	Description string   `json:"description,omitempty"`
	Flags       string   `json:"flags,omitempty"`
	Positional  bool     `json:"positional,omitempty"`
	Env         []EnvVar `json:"env,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

// Reference returns the command table with each command's flags.
func (p *Program) Reference() Reference {
	ref := Reference{Program: p.Name, Summary: p.Summary}
	for _, c := range p.All() {
		ref.Commands = append(ref.Commands, ReferenceCommand{
			Name:        c.Name,
			Summary:     c.Summary,
			Group:       c.Run == nil,
			Usage:       c.Usage,
			Description: c.Description,
			Flags:       p.Flags(&c),
			Positional:  c.Positional,
			Env:         c.Env,
			Examples:    c.Examples,
		})
	}
	return ref
}

// WriteJSON writes Reference as JSON.
func (p *Program) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(p.Reference())
}

func paragraphs(text string) []string {
	var out []string
	for _, para := range strings.Split(strings.TrimSpace(text), "\n\n") {
		if para = strings.TrimSpace(para); para != "" {
			out = append(out, para)
		}
	}
	return out
}

// wrap breaks text into lines of at most width bytes where it can.
func wrap(text string, width int) string {
	var b strings.Builder
	n := 0
	for _, word := range strings.Fields(text) {
		if n > 0 && n+1+len(word) > width {
			b.WriteByte('\n')
			n = 0
		} else if n > 0 {
			b.WriteByte(' ')
			n++
		}
		b.WriteString(word)
		n += len(word)
	}
	return b.String()
}

// suggest returns the candidate closest to name, or "" when none is close.
func suggest(name string, candidates []string) string {
	last := name[strings.LastIndex(name, " ")+1:]
	best, bestDist := "", 3
	for _, c := range candidates {
		word := c[strings.LastIndex(c, " ")+1:]
		d := distance(last, word)
		if d < bestDist && d < len(last) {
			best, bestDist = c, d
		}
	}
	return best
}

// distance is the edit distance between a and b, counting a swap of two
// neighbouring letters as one edit.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
