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

// HeaderField is one field of a message header. Value is the field body as
// it appears in the message, folding included.
type HeaderField struct {
	Name  string
	Value string
}

// AuthenticationOf reads SPF, DKIM, and DMARC from the Authentication-Results
// field that our own mail server wrote when it received the message.
//
// fields is the whole message header, in order, top first. The receiving
// server adds its trace fields, Received and Authentication-Results, at the
// top, above everything the sender wrote. The sender can also write an
// Authentication-Results field that names our server, and our server does not
// add one to every message: by default Stalwart adds none to mail that one of
// its own accounts submits. So the result comes from one field, and only when
// all of these hold:
//
//   - it is the top-most Authentication-Results field on the message;
//   - its authserv-id equals authservID, ignoring case;
//   - it is in the trace block at the top of the header, above the first
//     field that the author or a DKIM signer writes (see authorField); and
//   - that trace block has a Received field.
//
// Otherwise SPF, DKIM, and DMARC are empty. A malformed field has no
// authserv-id anyone can trust, so it reads as no results too. The rule holds
// when our server adds an Authentication-Results field to every message it
// accepts, or signs the mail its own accounts send; inbai init sets the first
// for Stalwart, and Stalwart does the second by default.
//
// When the field has more than one result for a method, such as one per DKIM
// signature, the value is pass if any of them passed, and the first result
// otherwise.
//
// junk sets Spam to "spam". A message that is not marked junk leaves Spam empty.
func AuthenticationOf(fields []HeaderField, authservID string, junk bool) Authentication {
	var out Authentication
	if junk {
		out.Spam = "spam"
	}
	authservID = strings.TrimSpace(authservID)
	if authservID == "" {
		return out
	}
	value, ok := traceResults(fields)
	if !ok {
		return out
	}
	id, results, ok := parseAuthResults(value)
	if !ok || !strings.EqualFold(id, authservID) {
		return out
	}
	out.SPF = pick(results["spf"])
	out.DKIM = pick(results["dkim"])
	out.DMARC = pick(results["dmarc"])
	return out
}

// traceResults returns the value of the top-most Authentication-Results field
// when it is in the trace block at the top of fields and that block has a
// Received field. The trace block ends at the first authorField.
func traceResults(fields []HeaderField) (string, bool) {
	var (
		value           string
		found, received bool
	)
	for _, field := range fields {
		name := strings.ToLower(strings.TrimSpace(field.Name))
		if authorField(name) {
			break
		}
		switch name {
		case "received":
			received = true
		case "authentication-results":
			if !found {
				value, found = field.Value, true
			}
		}
	}
	return value, found && received
}

// authorFields are the RFC 5322 originator, destination, identification, and
// informational fields, MIME-Version, and DKIM-Signature. The author or a
// server on the sender's side writes them, and a receiving server adds its
// trace fields above them. Our server signs only mail it sends, so a
// DKIM-Signature above an Authentication-Results field means our server did
// not write that field for an inbound hop.
var authorFields = map[string]struct{}{
	"from": {}, "sender": {}, "reply-to": {}, "to": {}, "cc": {}, "bcc": {},
	"message-id": {}, "in-reply-to": {}, "references": {},
	"subject": {}, "comments": {}, "keywords": {}, "date": {},
	"mime-version": {}, "dkim-signature": {},
}

// authorField reports whether the lower-case field name is one of
// authorFields or a Content- field.
func authorField(name string) bool {
	_, ok := authorFields[name]
	return ok || strings.HasPrefix(name, "content-")
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
