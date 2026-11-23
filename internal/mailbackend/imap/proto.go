// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package imap

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type item struct {
	text     string
	literals [][]byte
}

type session struct {
	r   *bufio.Reader
	w   io.Writer
	seq int
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
	s.seq++
	tag := fmt.Sprintf("a%03d", s.seq)
	if _, err := fmt.Fprintf(s.w, "%s %s\r\n", tag, strings.Join(parts, " ")); err != nil {
		return nil, err
	}
	var untagged []item
	for {
		got, err := s.readItem()
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(got.text, tag+" ") {
			if !strings.Contains(got.text, " OK") {
				return nil, fmt.Errorf("imap: %s", got.text)
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

func quote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
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
