// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
)

// Record is one mail-related entry from a Stalwart zone file.
type Record struct {
	Name  string
	Type  string
	Value string
}

// Finding is the result of comparing one record with public DNS.
type Finding struct {
	Name   string
	Type   string
	Status string
}

// Resolver looks up the records domain add checks.
type Resolver interface {
	LookupMX(context.Context, string) ([]*net.MX, error)
	LookupTXT(context.Context, string) ([]string, error)
}

// ParseZone reads a BIND zone file and returns its MX and TXT records.
func ParseZone(text string) ([]Record, error) {
	flat := flattenParentheses(text)
	origin := ""
	var records []Record
	for _, line := range strings.Split(flat, "\n") {
		line = stripComment(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "$") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && strings.EqualFold(fields[0], "$ORIGIN") {
				origin = normalizeName(fields[1])
			}
			continue
		}
		tokens := tokenize(line)
		if len(tokens) < 2 {
			continue
		}
		name := qualify(tokens[0].text, origin)
		i := 1
		if i < len(tokens) && isTTL(tokens[i].text) {
			i++
		}
		if i < len(tokens) && strings.EqualFold(tokens[i].text, "IN") {
			i++
		}
		if i >= len(tokens) {
			continue
		}
		kind := strings.ToUpper(tokens[i].text)
		i++
		if kind != "MX" && kind != "TXT" {
			continue
		}
		value, err := rdata(kind, tokens[i:])
		if err != nil {
			return nil, err
		}
		records = append(records, Record{Name: name, Type: kind, Value: value})
	}
	return records, nil
}

// Check compares MX and TXT records with a resolver.
func Check(ctx context.Context, records []Record, resolver Resolver) []Finding {
	var findings []Finding
	seen := map[string][]string{}
	mxSeen := map[string][]*net.MX{}
	mxErr := map[string]error{}
	txtErr := map[string]error{}
	for _, record := range records {
		if record.Type != "MX" && record.Type != "TXT" {
			continue
		}
		finding := Finding{Name: record.Name, Type: record.Type, Status: "not published"}
		switch record.Type {
		case "MX":
			hosts, ok := mxSeen[record.Name]
			err, looked := mxErr[record.Name]
			if !ok && !looked {
				hosts, err = resolver.LookupMX(ctx, record.Name)
				mxSeen[record.Name] = hosts
				mxErr[record.Name] = err
			}
			finding.Status = mxStatus(record.Value, hosts, err)
		case "TXT":
			values, ok := seen[record.Name]
			err, looked := txtErr[record.Name]
			if !ok && !looked {
				values, err = resolver.LookupTXT(ctx, record.Name)
				seen[record.Name] = values
				txtErr[record.Name] = err
			}
			finding.Status = txtStatus(record.Value, values, err)
		}
		findings = append(findings, finding)
	}
	return findings
}

func mxStatus(want string, hosts []*net.MX, err error) string {
	if notFound(err) {
		return "not published"
	}
	if err != nil {
		return "lookup failed"
	}
	for _, host := range hosts {
		got := fmt.Sprintf("%d %s", host.Pref, normalizeName(host.Host))
		if got == want {
			return "published"
		}
	}
	if len(hosts) == 0 {
		return "not published"
	}
	return "mismatch"
}

func txtStatus(want string, values []string, err error) string {
	if notFound(err) {
		return "not published"
	}
	if err != nil {
		return "lookup failed"
	}
	for _, value := range values {
		if value == want {
			return "published"
		}
	}
	if len(values) == 0 {
		return "not published"
	}
	return "mismatch"
}

func notFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

type word struct {
	text   string
	quoted bool
}

func tokenize(line string) []word {
	var words []word
	for i := 0; i < len(line); {
		if unicode.IsSpace(rune(line[i])) {
			i++
			continue
		}
		if line[i] == '"' {
			j := i + 1
			var b strings.Builder
			for j < len(line) && line[j] != '"' {
				b.WriteByte(line[j])
				j++
			}
			words = append(words, word{text: b.String(), quoted: true})
			i = j + 1
			continue
		}
		j := i
		for j < len(line) && !unicode.IsSpace(rune(line[j])) {
			j++
		}
		words = append(words, word{text: line[i:j]})
		i = j
	}
	return words
}

func rdata(kind string, tokens []word) (string, error) {
	if len(tokens) == 0 {
		return "", fmt.Errorf("zone record %s has no value", kind)
	}
	if kind == "TXT" {
		var b strings.Builder
		for _, token := range tokens {
			b.WriteString(token.text)
		}
		return b.String(), nil
	}
	parts := make([]string, len(tokens))
	for i, token := range tokens {
		parts[i] = token.text
	}
	if kind == "MX" && len(parts) >= 2 {
		return parts[0] + " " + normalizeName(parts[1]), nil
	}
	return strings.Join(parts, " "), nil
}

func qualify(name, origin string) string {
	if name == "@" {
		return origin
	}
	if strings.HasSuffix(name, ".") {
		return normalizeName(name)
	}
	if origin == "" {
		return normalizeName(name)
	}
	return normalizeName(name + "." + origin)
}

func normalizeName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

func isTTL(text string) bool {
	_, err := strconv.Atoi(text)
	return err == nil
}

func flattenParentheses(text string) string {
	var b strings.Builder
	depth := 0
	for _, r := range text {
		switch r {
		case '(':
			depth++
			b.WriteByte(' ')
		case ')':
			if depth > 0 {
				depth--
			}
			b.WriteByte(' ')
		case '\n':
			if depth > 0 {
				b.WriteByte(' ')
			} else {
				b.WriteByte('\n')
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func stripComment(line string) string {
	var b strings.Builder
	quoted := false
	for i := 0; i < len(line); i++ {
		if line[i] == '"' {
			quoted = !quoted
		}
		if line[i] == ';' && !quoted {
			break
		}
		b.WriteByte(line[i])
	}
	return strings.TrimSpace(b.String())
}
