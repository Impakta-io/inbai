// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

// Command docscheck checks public Markdown for a status line, broken relative links, and planning-note leaks.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const forbidden = ".private"

var linkRE = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)`)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	problems := check(root)
	for _, problem := range problems {
		fmt.Println(problem)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
}

func check(root string) []string {
	var problems []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if entry.IsDir() {
			if name == ".git" || name == ".private" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		if strings.Contains(text, forbidden) {
			problems = append(problems, rel+": mentions a gitignored planning path")
		}
		if !strings.Contains(text, "Status:") {
			problems = append(problems, rel+": missing status line")
		}
		problems = append(problems, links(path, rel, text)...)
		return nil
	})
	if err != nil {
		problems = append(problems, err.Error())
	}
	return problems
}

func links(path, rel, text string) []string {
	var problems []string
	for _, match := range linkRE.FindAllStringSubmatch(text, -1) {
		raw := match[1]
		if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "mailto:") {
			continue
		}
		target, fragment, _ := strings.Cut(raw, "#")
		dest := path
		if target != "" {
			dest = filepath.Clean(filepath.Join(filepath.Dir(path), target))
			if _, err := os.Stat(dest); err != nil {
				problems = append(problems, rel+": broken link "+raw)
				continue
			}
			if !strings.HasSuffix(dest, ".md") {
				continue
			}
		}
		if fragment == "" {
			continue
		}
		linked, err := os.ReadFile(dest)
		if err != nil {
			problems = append(problems, rel+": broken link "+raw)
			continue
		}
		if !hasHeading(string(linked), fragment) {
			problems = append(problems, rel+": missing heading #"+fragment)
		}
	}
	return problems
}

func hasHeading(text, fragment string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		title := strings.TrimSpace(strings.TrimLeft(line, "#"))
		if slug(title) == fragment {
			return true
		}
	}
	return false
}

func slug(title string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
			hyphen = false
			continue
		}
		if (r == ' ' || r == '-') && b.Len() > 0 && !hyphen {
			b.WriteByte('-')
			hyphen = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
