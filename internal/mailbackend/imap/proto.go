// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package imap

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

type item struct {
	text     string
	literals [][]byte
}

// maxLiteral is the largest literal the client will read from a server. The
// API accepts a 16 MiB JSON body and the Stalwart backend reads up to 25 MiB,
// so 64 MiB is above any message the rest of the system handles and still
// stops a server from making the client allocate gigabytes.
const maxLiteral = 64 << 20

type session struct {
	r      *bufio.Reader
	w      io.Writer
	seq    int
	broken error
}

// closedError is a connection the server ended while the client waited for the
// reply to step.
type closedError struct {
	step  string
	cause error
}

func (e *closedError) Error() string {
	return "imap: server closed the connection during " + e.step
}

func (e *closedError) Unwrap() error { return e.cause }

// statusError is a tagged NO or BAD reply. text is the server's own line.
type statusError struct{ text string }

func (e *statusError) Error() string { return "imap: " + e.text }

// stepName names a command for an error message, for example "SELECT" or
// "UID FETCH".
func stepName(parts []string) string {
	if len(parts) > 1 && strings.EqualFold(parts[0], "UID") {
		return "UID " + parts[1]
	}
	return parts[0]
}

func connectionEnded(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, net.ErrClosed)
}

func (s *session) greet() error {
	item, err := s.readItem()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(item.text, "* ") {
		return fmt.Errorf("imap: unexpected greeting")
	}
	return nil
}

func (s *session) command(parts ...string) ([]item, error) {
	if s.broken != nil {
		return nil, s.broken
	}
	s.seq++
	tag := fmt.Sprintf("a%03d", s.seq)
	if _, err := fmt.Fprintf(s.w, "%s %s\r\n", tag, strings.Join(parts, " ")); err != nil {
		return nil, err
	}
	var untagged []item
	for {
		got, err := s.readItem()
		if err != nil {
			if connectionEnded(err) {
				err = &closedError{step: stepName(parts), cause: err}
			}
			s.broken = err
			return nil, err
		}
		if strings.HasPrefix(got.text, tag+" ") {
			status, _, _ := strings.Cut(strings.TrimPrefix(got.text, tag+" "), " ")
			if !strings.EqualFold(status, "OK") {
				return nil, &statusError{text: got.text}
			}
			return untagged, nil
		}
		untagged = append(untagged, got)
	}
}

func (s *session) readItem() (item, error) {
	line, err := s.r.ReadString('\n')
	if err != nil {
		return item{}, err
	}
	line = strings.TrimRight(line, "\r\n")
	n, ok := literalSize(line)
	if !ok {
		return item{text: line}, nil
	}
	if n > maxLiteral {
		return item{}, fmt.Errorf("imap: server sent a %d byte literal, more than the %d byte limit", n, maxLiteral)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(s.r, buf); err != nil {
		return item{}, err
	}
	rest, err := s.r.ReadString('\n')
	if err != nil {
		return item{}, err
	}
	text := line[:strings.LastIndex(line, "{")] + strings.TrimRight(rest, "\r\n")
	return item{text: text, literals: [][]byte{buf}}, nil
}

func literalSize(line string) (int, bool) {
	start := strings.LastIndex(line, "{")
	if start < 0 || !strings.HasSuffix(line, "}") {
		return 0, false
	}
	n, err := strconv.Atoi(line[start+1 : len(line)-1])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// quote returns value as an IMAP quoted string. A quoted string cannot hold
// CR, LF, or NUL, so quote refuses them. Writing one would end the command and
// start another on the same logged-in connection.
func quote(value string) (string, error) {
	if err := noLineBreaks("imap argument", value); err != nil {
		return "", err
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`, nil
}

// noLineBreaks refuses a value with CR, LF, or NUL.
func noLineBreaks(what, value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%w: %s holds a line break or NUL", mailbackend.ErrInvalid, what)
	}
	return nil
}

func searchUIDs(items []item) []int {
	for _, item := range items {
		if !strings.Contains(item.text, " SEARCH") {
			continue
		}
		fields := strings.Fields(item.text)
		var uids []int
		for _, field := range fields[2:] {
			uid, err := strconv.Atoi(field)
			if err != nil {
				continue
			}
			uids = append(uids, uid)
		}
		return uids
	}
	return nil
}

func statusOf(items []item) (int, int, error) {
	for _, item := range items {
		if !strings.Contains(item.text, "STATUS") {
			continue
		}
		validity := numberAfter(item.text, "UIDVALIDITY")
		next := numberAfter(item.text, "UIDNEXT")
		if validity == 0 || next == 0 {
			return 0, 0, fmt.Errorf("imap: status response is missing uidvalidity")
		}
		return validity, next, nil
	}
	return 0, 0, fmt.Errorf("imap: status response is missing")
}

func numberAfter(text, key string) int {
	fields := strings.Fields(strings.NewReplacer("(", " ", ")", " ").Replace(text))
	for i, field := range fields {
		if strings.EqualFold(field, key) && i+1 < len(fields) {
			n, _ := strconv.Atoi(fields[i+1])
			return n
		}
	}
	return 0
}

func fetched(items []item) map[int]item {
	out := map[int]item{}
	for _, item := range items {
		if !strings.Contains(item.text, "FETCH") {
			continue
		}
		uid := numberAfter(item.text, "UID")
		if uid > 0 {
			out[uid] = item
		}
	}
	return out
}

func subjectOf(header []byte) string {
	for _, line := range strings.Split(string(header), "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) >= 8 && strings.EqualFold(line[:8], "subject:") {
			return strings.TrimSpace(line[8:])
		}
	}
	return ""
}

// internalDate reads the quoted date-time after INTERNALDATE. The RFC 3501
// date-time allows a space-padded day, so the value is trimmed before parsing.
func internalDate(text string) time.Time {
	upper := strings.ToUpper(text)
	at := strings.Index(upper, "INTERNALDATE")
	if at < 0 {
		return time.Time{}
	}
	rest := strings.TrimLeft(text[at+len("INTERNALDATE"):], " ")
	if !strings.HasPrefix(rest, `"`) {
		return time.Time{}
	}
	raw, _, ok := strings.Cut(rest[1:], `"`)
	if !ok {
		return time.Time{}
	}
	parsed, err := time.Parse("2-Jan-2006 15:04:05 -0700", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

// flagsOf returns the flags in the FLAGS (...) list of a FETCH response.
func flagsOf(text string) []string {
	at := strings.Index(strings.ToUpper(text), "FLAGS (")
	if at < 0 {
		return nil
	}
	list, _, ok := strings.Cut(text[at+len("FLAGS ("):], ")")
	if !ok {
		return nil
	}
	return strings.Fields(list)
}

// isJunk reports whether the flags mark a message as spam. $Junk is the
// standard keyword (RFC 5788). \Junk and Junk are what some servers use.
func isJunk(flags []string) bool {
	for _, flag := range flags {
		switch strings.ToLower(flag) {
		case `$junk`, `\junk`, `junk`:
			return true
		}
	}
	return false
}

func formatState(validity, next int) string {
	return strconv.Itoa(validity) + ":" + strconv.Itoa(next)
}

func parseState(state string) (int, int, error) {
	validity, next, ok := strings.Cut(state, ":")
	if !ok {
		return 0, 0, fmt.Errorf("imap: cursor %q is not uidvalidity:uidnext", state)
	}
	left, err := strconv.Atoi(validity)
	if err != nil {
		return 0, 0, fmt.Errorf("imap: cursor %q is not uidvalidity:uidnext", state)
	}
	right, err := strconv.Atoi(next)
	if err != nil {
		return 0, 0, fmt.Errorf("imap: cursor %q is not uidvalidity:uidnext", state)
	}
	return left, right, nil
}
