// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import "strings"

// Authentication is what the mail server recorded about a message.
// An empty field means that result was not on the message.
type Authentication struct {
	SPF   string `json:"spf,omitempty"`
	DKIM  string `json:"dkim,omitempty"`
	DMARC string `json:"dmarc,omitempty"`
	Spam  string `json:"spam,omitempty"`
}

var authValues = map[string]struct{}{
	"pass": {}, "fail": {}, "softfail": {}, "neutral": {}, "none": {},
	"temperror": {}, "permerror": {}, "policy": {}, "unknown": {}, "bestguesspass": {},
}

// AuthenticationOf reads SPF, DKIM, and DMARC from the Authentication-Results
// header that our own mail server wrote.
//
// headers are the values of every Authentication-Results field on the message,
// in header order, top first. Any server on the path, and the sender, can add
// one of these fields, so only the top-most one whose authserv-id equals
// authservID is read. The comparison ignores case and is otherwise exact.
// Every other instance is ignored. When authservID is empty or no header
// matches, SPF, DKIM, and DMARC are empty. A malformed header has no
// authserv-id anyone can trust, so reading stops there and they are empty too.
//
// When the header has more than one result for a method, such as one per DKIM
// signature, the value is pass if any of them passed, and the first result
// otherwise.
//
// junk sets Spam to "spam". A message that is not marked junk leaves Spam empty.
func AuthenticationOf(headers []string, authservID string, junk bool) Authentication {
	var out Authentication
	if junk {
		out.Spam = "spam"
	}
	authservID = strings.TrimSpace(authservID)
	if authservID == "" {
		return out
	}
	for _, header := range headers {
		id, results, ok := parseAuthResults(header)
		if !ok {
			return out
		}
		if !strings.EqualFold(id, authservID) {
			continue
		}
		out.SPF = pick(results["spf"])
		out.DKIM = pick(results["dkim"])
		out.DMARC = pick(results["dmarc"])
		return out
	}
	return out
}

func pick(results []string) string {
	for _, result := range results {
		if result == "pass" {
			return result
		}
	}
	if len(results) == 0 {
		return ""
	}
	return results[0]
}

// parseAuthResults reads one Authentication-Results field value as RFC 8601
// section 2.2 defines it:
//
//	authserv-id [ version ] ( "; none" / 1*( "; " method[/version] "=" result [ reason ] [ props ] ) )
//
// Comments in parentheses are removed first, and quoted strings are kept whole,
// so a ";" or "=" inside either does not start a new result. It returns the
// authserv-id and, for each method name in lower case, the results in the order
// they appear. A result word that is not a known result is dropped. ok is false
// when the value is malformed or names an unknown version.
func parseAuthResults(value string) (id string, results map[string][]string, ok bool) {
	segments, ok := lexAuthResults(value)
	if !ok || len(segments) == 0 {
		return "", nil, false
	}
	head := segments[0]
	if len(head) == 0 || len(head) > 2 || head[0].punct() {
		return "", nil, false
	}
	if len(head) == 2 && head[1].text != "1" {
		return "", nil, false
	}
	id = head[0].text
	results = map[string][]string{}
	for _, seg := range segments[1:] {
		method, result, ok := methodResult(seg)
		if !ok {
			continue
		}
		if _, known := authValues[result]; !known {
			continue
		}
		results[method] = append(results[method], result)
	}
	return id, results, true
}

// methodResult reads the methodspec at the start of one resinfo:
// method [ "/" version ] "=" result. Whatever follows, reason= and properties,
// is not needed and is skipped.
func methodResult(seg []authToken) (method, result string, ok bool) {
	if len(seg) < 3 || !seg[0].word() {
		return "", "", false
	}
	method = strings.ToLower(seg[0].text)
	i := 1
	if seg[i].is("/") {
		if len(seg) < i+4 || !seg[i+1].word() {
			return "", "", false
		}
		i += 2
	}
	if !seg[i].is("=") || i+1 >= len(seg) || !seg[i+1].word() {
		return "", "", false
	}
	return method, strings.ToLower(seg[i+1].text), true
}

type authToken struct {
	text   string
	quoted bool
}

func (t authToken) punct() bool      { return !t.quoted && (t.text == "=" || t.text == "/") }
func (t authToken) word() bool       { return !t.quoted && !t.punct() }
func (t authToken) is(p string) bool { return !t.quoted && t.text == p }

// lexAuthResults splits a field value into ";"-separated segments of tokens.
// A token is a run of characters other than space, tab, CR, LF, "(", ")",
// quote, ";", "=", and "/"; a quoted string; or one "=" or "/". Comments,
// which may nest and use backslash escapes, separate tokens and are dropped.
// An unterminated comment or quoted string makes the value malformed.
func lexAuthResults(value string) ([][]authToken, bool) {
	var (
		segments = [][]authToken{nil}
		word     strings.Builder
	)
	flush := func() {
		if word.Len() > 0 {
			last := len(segments) - 1
			segments[last] = append(segments[last], authToken{text: word.String()})
			word.Reset()
		}
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch c {
		case ' ', '\t', '\r', '\n':
			flush()
		case '(':
			flush()
			depth := 1
			for i++; i < len(value) && depth > 0; i++ {
				switch value[i] {
				case '\\':
					i++
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			if depth > 0 {
				return nil, false
			}
			i--
		case ')':
			return nil, false
		case '"':
			flush()
			var quoted strings.Builder
			closed := false
			for i++; i < len(value); i++ {
				if value[i] == '\\' && i+1 < len(value) {
					i++
					quoted.WriteByte(value[i])
					continue
				}
				if value[i] == '"' {
					closed = true
					break
				}
				quoted.WriteByte(value[i])
			}
			if !closed {
				return nil, false
			}
			last := len(segments) - 1
			segments[last] = append(segments[last], authToken{text: quoted.String(), quoted: true})
		case ';':
			flush()
			segments = append(segments, nil)
		case '=', '/':
			flush()
			last := len(segments) - 1
			segments[last] = append(segments[last], authToken{text: string(c)})
		default:
			word.WriteByte(c)
		}
	}
	flush()
	return segments, true
}
