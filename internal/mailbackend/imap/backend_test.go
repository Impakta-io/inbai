// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package imap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

var (
	_ mailbackend.Backend = (*Backend)(nil)
	_ mailbackend.Watcher = (*Backend)(nil)
)

func TestCreateInboxIsNotSupported(t *testing.T) {
	backend := &Backend{}
	if _, err := backend.CreateInbox(context.Background(), mailbackend.InboxSpec{Local: "ada"}); err == nil {
		t.Fatal("provisioned an account")
	}
	if _, err := backend.CreateInboxes(context.Background(), nil); err == nil {
		t.Fatal("provisioned accounts")
	}
	if _, _, err := backend.List(context.Background(), "missing", mailbackend.Page{}); err == nil {
		t.Fatal("listed an unknown account")
	}
}

func TestOpenReadsTheAccountFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`{"ada":{"user":"ada@example.com","password":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backend, err := Open(Config{IMAP: "127.0.0.1:143", SMTP: "127.0.0.1:587", Accounts: path})
	if err != nil {
		t.Fatal(err)
	}
	if backend.accounts["ada"].User != "ada@example.com" {
		t.Fatalf("user %q", backend.accounts["ada"].User)
	}
	if _, err := Open(Config{}); err == nil {
		t.Fatal("opened an empty config")
	}
}

func TestListGetDeleteAndChanges(t *testing.T) {
	addr := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		write := bufio.NewWriter(conn)
		read := bufio.NewReader(conn)
		say(write, "* OK ready\r\n")
		_ = write.Flush()
		var state connState
		for {
			line, err := read.ReadString('\n')
			if err != nil {
				return
			}
			tag, cmd, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
			if refusal := state.step(cmd); refusal != "" {
				say(write, "%s BAD %s\r\n", tag, refusal)
				_ = write.Flush()
				continue
			}
			switch {
			case strings.HasPrefix(cmd, "LOGIN"), strings.HasPrefix(cmd, "SELECT"), strings.HasPrefix(cmd, "EXAMINE"), strings.HasPrefix(cmd, "UID STORE"):
				say(write, "%s OK\r\n", tag)
			case cmd == "UID SEARCH ALL" || strings.HasPrefix(cmd, "UID SEARCH TEXT"):
				say(write, "* SEARCH 8\r\n%s OK\r\n", tag)
			case strings.Contains(cmd, "AUTHENTICATION-RESULTS"):
				if !strings.Contains(cmd, "REPLY-TO") {
					t.Errorf("get did not ask for Reply-To: %s", cmd)
				}
				header := "Authentication-Results: mail.example.com; spf=pass; dkim=pass; dmarc=fail\r\nFrom: Ada <ada@example.com>\r\nReply-To: Help <help@example.com>\r\n"
				say(write, "* 1 FETCH (UID 8 FLAGS (\\Junk) BODY[HEADER.FIELDS (AUTHENTICATION-RESULTS)] {%d}\r\n%s)\r\n%s OK\r\n", len(header), header, tag)
			case strings.HasPrefix(cmd, "UID FETCH") && strings.Contains(cmd, "HEADER"):
				header := "Subject: Hello\r\n\r\n"
				say(write, "* 1 FETCH (UID 8 BODY[HEADER.FIELDS (SUBJECT)] {%d}\r\n%s)\r\n%s OK\r\n", len(header), header, tag)
			case strings.HasPrefix(cmd, "UID FETCH"):
				say(write, "* 1 FETCH (UID 8 BODY[TEXT] {4}\r\nbody)\r\n%s OK\r\n", tag)
			case strings.HasPrefix(cmd, "UID EXPUNGE"):
				say(write, "* 1 EXPUNGE\r\n%s OK\r\n", tag)
			case strings.HasPrefix(cmd, "STATUS"):
				say(write, "* STATUS INBOX (UIDVALIDITY 4 UIDNEXT 9)\r\n%s OK\r\n", tag)
			case strings.HasPrefix(cmd, "UID SEARCH UID"):
				say(write, "* SEARCH 8\r\n%s OK\r\n", tag)
			case strings.HasPrefix(cmd, "LOGOUT"):
				say(write, "* BYE\r\n%s OK\r\n", tag)
				_ = write.Flush()
				return
			default:
				say(write, "%s BAD %s\r\n", tag, cmd)
			}
			_ = write.Flush()
		}
	})
	backend := &Backend{imap: addr, accounts: map[string]Login{"ada": {User: "ada@example.com", Password: "secret"}}}
	ctx := context.Background()
	messages, _, err := backend.List(ctx, "ada", mailbackend.Page{})
	if err != nil || len(messages) != 1 || messages[0].ID != "8" || messages[0].Subject != "Hello" {
		t.Fatalf("list %+v err %v", messages, err)
	}
	found, _, err := backend.Search(ctx, "ada", "Hello", mailbackend.Page{})
	if err != nil || len(found) != 1 {
		t.Fatalf("search %+v err %v", found, err)
	}
	message, err := backend.Get(ctx, "ada", "8")
	if err != nil || message.Text != "body" || message.From != "Ada <ada@example.com>" || message.ReplyTo != "Help <help@example.com>" || message.Authentication.SPF != "pass" || message.Authentication.DMARC != "fail" || message.Authentication.Spam != "spam" {
		t.Fatalf("get %+v err %v", message, err)
	}
	if err := backend.Delete(ctx, "ada", "8"); err != nil {
		t.Fatal(err)
	}
	state, err := backend.MailState(ctx, "ada")
	if err != nil || state != "4:9" {
		t.Fatalf("state %s err %v", state, err)
	}
	delta, err := backend.MailChanges(ctx, "ada", "4:8")
	if err != nil || delta.State != "4:9" || len(delta.Created) != 1 || delta.Created[0] != "8" {
		t.Fatalf("delta %+v err %v", delta, err)
	}
}

func TestMailChangesExaminesTheMailboxBeforeSearching(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	addr := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		write := bufio.NewWriter(conn)
		read := bufio.NewReader(conn)
		say(write, "* OK ready\r\n")
		_ = write.Flush()
		var state connState
		for {
			line, err := read.ReadString('\n')
			if err != nil {
				return
			}
			tag, cmd, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
			mu.Lock()
			seen = append(seen, cmd)
			mu.Unlock()
			if refusal := state.step(cmd); refusal != "" {
				say(write, "%s BAD %s\r\n", tag, refusal)
			} else if strings.HasPrefix(cmd, "STATUS") {
				say(write, "* STATUS INBOX (UIDVALIDITY 4 UIDNEXT 9)\r\n%s OK\r\n", tag)
			} else if strings.HasPrefix(cmd, "UID SEARCH") {
				say(write, "* SEARCH 8\r\n%s OK\r\n", tag)
			} else {
				say(write, "%s OK\r\n", tag)
			}
			_ = write.Flush()
			if strings.HasPrefix(cmd, "LOGOUT") {
				return
			}
		}
	})
	backend := &Backend{imap: addr, accounts: map[string]Login{"ada": {User: "ada", Password: "secret"}}}
	delta, err := backend.MailChanges(context.Background(), "ada", "4:8")
	if err != nil || len(delta.Created) != 1 || delta.Created[0] != "8" {
		t.Fatalf("delta %+v err %v", delta, err)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(seen, "|")
	if !strings.Contains(joined, "EXAMINE INBOX|UID SEARCH") {
		t.Fatalf("commands %s", joined)
	}
}

func TestTaggedNoWithOKInTheTextIsAFailure(t *testing.T) {
	for _, status := range []string{"NO mailbox is OK to delete? no", "BAD OK", "NO"} {
		s := &session{r: bufio.NewReader(strings.NewReader("a001 " + status + "\r\n")), w: io.Discard}
		if _, err := s.command("NOOP"); err == nil {
			t.Fatalf("%q was treated as success", status)
		}
	}
	s := &session{r: bufio.NewReader(strings.NewReader("a001 OK done\r\n")), w: io.Discard}
	if _, err := s.command("NOOP"); err != nil {
		t.Fatal(err)
	}
}

func TestHugeLiteralIsRefusedWithoutAllocating(t *testing.T) {
	s := &session{r: bufio.NewReader(strings.NewReader("* 1 FETCH (UID 1 BODY[TEXT] {99999999999}\r\n")), w: io.Discard}
	_, err := s.command("UID", "FETCH", "1", "(UID BODY.PEEK[TEXT])")
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err %v", err)
	}
	if _, err := s.command("LOGOUT"); err == nil {
		t.Fatal("reused a connection that failed mid-response")
	}
	just := &session{r: bufio.NewReader(strings.NewReader("* 1 FETCH (UID 1 BODY[TEXT] {" + strconv.Itoa(maxLiteral+1) + "}\r\n")), w: io.Discard}
	if _, err := just.command("NOOP"); err == nil {
		t.Fatal("accepted a literal over the cap")
	}
}

func TestJunkKeywords(t *testing.T) {
	for flags, want := range map[string]bool{`\Seen $Junk`: true, `\Junk`: true, `Junk`: true, `$junk`: true, `\Seen $NotJunk`: false, ``: false} {
		if got := isJunk(strings.Fields(flags)); got != want {
			t.Errorf("%q: got %v", flags, got)
		}
	}
	text := `* 1 FETCH (UID 8 FLAGS (\Seen $Junk) BODY[HEADER.FIELDS (FROM)] {5}`
	if !isJunk(flagsOf(text)) {
		t.Fatal("did not read $Junk from a FETCH response")
	}
	if isJunk(flagsOf(`* 1 FETCH (UID 8 FLAGS () BODY[TEXT] {5}`)) {
		t.Fatal("empty flags were junk")
	}
}

func TestUIDValidityChangeDoesNotAdvance(t *testing.T) {
	addr := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		write := bufio.NewWriter(conn)
		read := bufio.NewReader(conn)
		say(write, "* OK ready\r\n")
		_ = write.Flush()
		var state connState
		for {
			line, err := read.ReadString('\n')
			if err != nil {
				return
			}
			tag, cmd, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
			if refusal := state.step(cmd); refusal != "" {
				say(write, "%s BAD %s\r\n", tag, refusal)
				_ = write.Flush()
				continue
			}
			if strings.HasPrefix(cmd, "STATUS") {
				say(write, "* STATUS INBOX (UIDVALIDITY 9 UIDNEXT 1)\r\n%s OK\r\n", tag)
			} else if strings.HasPrefix(cmd, "LOGOUT") {
				say(write, "%s OK\r\n", tag)
				_ = write.Flush()
				return
			} else {
				say(write, "%s OK\r\n", tag)
			}
			_ = write.Flush()
		}
	})
	backend := &Backend{imap: addr, accounts: map[string]Login{"ada": {User: "ada", Password: "secret"}}}
	if _, err := backend.MailChanges(context.Background(), "ada", "4:8"); !errors.Is(err, mailbackend.ErrChangesLost) {
		t.Fatalf("a new uidvalidity gave %v, want ErrChangesLost", err)
	}
}

func TestDeliverSubmitsSMTP(t *testing.T) {
	var data string
	addr := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		write := bufio.NewWriter(conn)
		read := bufio.NewReader(conn)
		say(write, "220 ready\r\n")
		_ = write.Flush()
		for {
			line, err := read.ReadString('\n')
			if err != nil {
				return
			}
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				say(write, "250-localhost\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(upper, "AUTH"):
				say(write, "235 ok\r\n")
			case strings.HasPrefix(upper, "DATA"):
				say(write, "354 go\r\n")
				_ = write.Flush()
				var body strings.Builder
				for {
					part, err := read.ReadString('\n')
					if err != nil {
						return
					}
					if part == ".\r\n" {
						break
					}
					body.WriteString(part)
				}
				data = body.String()
				say(write, "250 ok\r\n")
			case strings.HasPrefix(upper, "QUIT"):
				say(write, "221 bye\r\n")
				_ = write.Flush()
				return
			default:
				say(write, "250 ok\r\n")
			}
			_ = write.Flush()
		}
	})
	backend := &Backend{smtp: addr, accounts: map[string]Login{"ada": {User: "ada@example.com", Password: "secret"}}}
	err := backend.Deliver(context.Background(), mailbackend.Outbound{
		AccountID: "ada", From: "ada@example.com", To: "bob@example.com", Subject: "Hello", Body: "text",
		Attachments: []mailbackend.File{{Name: "note.txt", Type: "text/plain", Data: []byte("file")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, "Subject: Hello") || !strings.Contains(data, "filename=\"note.txt\"") || !strings.Contains(data, "ZmlsZQ==") {
		t.Fatalf("message %s", data)
	}
}

func listen(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(conn)
		}
	}()
	return ln.Addr().String()
}

// connState enforces the RFC 3501 states: SEARCH, FETCH, STORE and EXPUNGE
// are valid only after SELECT or EXAMINE, and nothing but LOGIN works before
// LOGIN.
type connState struct {
	loggedIn bool
	selected bool
}

// step records cmd and returns a refusal text, or "" when the command is valid
// in the current state.
func (c *connState) step(cmd string) string {
	verb, _, _ := strings.Cut(cmd, " ")
	if verb == "UID" {
		verb = "UID " + strings.Fields(cmd)[1]
	}
	switch verb {
	case "LOGIN":
		c.loggedIn = true
	case "LOGOUT", "NOOP":
	case "STATUS":
		if !c.loggedIn {
			return "not authenticated"
		}
	case "SELECT", "EXAMINE":
		if !c.loggedIn {
			return "not authenticated"
		}
		c.selected = true
	default:
		if !c.selected {
			return "command needs a selected mailbox: " + verb
		}
	}
	return ""
}

func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func TestSearchRefusesLineBreaksAndWritesNothing(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	addr := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		write := bufio.NewWriter(conn)
		read := bufio.NewReader(conn)
		say(write, "* OK ready\r\n")
		_ = write.Flush()
		for {
			line, err := read.ReadString('\n')
			if err != nil {
				return
			}
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
			tag, cmd, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
			if strings.HasPrefix(cmd, "UID SEARCH") {
				say(write, "* SEARCH\r\n")
			}
			say(write, "%s OK\r\n", tag)
			_ = write.Flush()
		}
	})
	backend := &Backend{imap: addr, accounts: map[string]Login{"ada": {User: "ada@example.com", Password: "secret"}}}
	for _, query := range []string{"a\r\nb001 UID STORE 1 +FLAGS (\\Deleted)", "a\nb", "a\rb", "a\x00b"} {
		_, _, err := backend.Search(context.Background(), "ada", query, mailbackend.Page{})
		if !errors.Is(err, mailbackend.ErrInvalid) {
			t.Fatalf("query %q: err %v", query, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 0 {
		t.Fatalf("wrote to the server: %q", lines)
	}
}

func TestSearchQuotesAPlainQuery(t *testing.T) {
	got, err := quote(`say "hi" \ there`)
	if err != nil || got != `"say \"hi\" \\ there"` {
		t.Fatalf("got %s err %v", got, err)
	}
}

func TestDeliverRefusesLineBreaksInAttachmentType(t *testing.T) {
	var mu sync.Mutex
	connected := false
	addr := listen(t, func(conn net.Conn) {
		mu.Lock()
		connected = true
		mu.Unlock()
		_ = conn.Close()
	})
	backend := &Backend{smtp: addr, accounts: map[string]Login{"ada": {User: "ada@example.com", Password: "secret"}}}
	for _, kind := range []string{"text/plain\r\nX-Evil: 1", "text/plain\nX-Evil: 1", "text/plain\rX", "text/plain\x00"} {
		err := backend.Deliver(context.Background(), mailbackend.Outbound{
			AccountID: "ada", From: "ada@example.com", To: "bob@example.com", Subject: "Hello", Body: "text",
			Attachments: []mailbackend.File{{Name: "note.txt", Type: kind, Data: []byte("file")}},
		})
		if !errors.Is(err, mailbackend.ErrInvalid) {
			t.Fatalf("type %q: err %v", kind, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if connected {
		t.Fatal("opened a connection for a refused message")
	}
}

func TestSenderAddressesIsTheLoginAddress(t *testing.T) {
	backend := &Backend{accounts: map[string]Login{
		"ada": {User: "ada@example.com", Password: "secret"},
		"bea": {User: "bea", Password: "secret"},
	}}
	ctx := context.Background()
	got, err := backend.SenderAddresses(ctx, "ada")
	if err != nil || len(got) != 1 || got[0] != "ada@example.com" {
		t.Fatalf("addresses %v err %v", got, err)
	}
	if _, err := backend.SenderAddresses(ctx, "bea"); err == nil || !strings.Contains(err.Error(), "not an email address") {
		t.Fatalf("bare login err %v", err)
	}
	if _, err := backend.SenderAddresses(ctx, "missing"); err == nil {
		t.Fatal("an unknown account has sender addresses")
	}
}

func TestListIsNewestFirstAndPagesPastNewArrivals(t *testing.T) {
	var mu sync.Mutex
	uids := "3 1 5 2 4" // the server's order does not matter
	addr := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		write := bufio.NewWriter(conn)
		read := bufio.NewReader(conn)
		say(write, "* OK ready\r\n")
		_ = write.Flush()
		for {
			line, err := read.ReadString('\n')
			if err != nil {
				return
			}
			tag, cmd, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
			switch {
			case strings.HasPrefix(cmd, "UID SEARCH"):
				mu.Lock()
				say(write, "* SEARCH %s\r\n%s OK\r\n", uids, tag)
				mu.Unlock()
			case strings.HasPrefix(cmd, "UID FETCH"):
				for _, uid := range strings.Split(strings.Fields(cmd)[2], ",") {
					header := "Subject: s" + uid + "\r\n\r\n"
					say(write, "* 1 FETCH (UID %s BODY[HEADER.FIELDS (SUBJECT)] {%d}\r\n%s)\r\n", uid, len(header), header)
				}
				say(write, "%s OK\r\n", tag)
			case strings.HasPrefix(cmd, "LOGOUT"):
				say(write, "* BYE\r\n%s OK\r\n", tag)
				_ = write.Flush()
				return
			default:
				say(write, "%s OK\r\n", tag)
			}
			_ = write.Flush()
		}
	})
	backend := &Backend{imap: addr, accounts: map[string]Login{"ada": {User: "ada@example.com", Password: "secret"}}}
	ctx := context.Background()
	ids := func(messages []mailbackend.Message) string {
		var out []string
		for _, m := range messages {
			out = append(out, m.ID)
		}
		return strings.Join(out, ",")
	}
	first, next, err := backend.List(ctx, "ada", mailbackend.Page{Limit: 2})
	if err != nil || ids(first) != "5,4" || next != "4" {
		t.Fatalf("first %s next %q err %v", ids(first), next, err)
	}
	mu.Lock()
	uids += " 6" // a message arrives between pages
	mu.Unlock()
	second, next, err := backend.List(ctx, "ada", mailbackend.Page{After: next, Limit: 2})
	if err != nil || ids(second) != "3,2" || next != "2" {
		t.Fatalf("second %s next %q err %v", ids(second), next, err)
	}
	last, next, err := backend.Search(ctx, "ada", "x", mailbackend.Page{After: next, Limit: 2})
	if err != nil || ids(last) != "1" || next != "" {
		t.Fatalf("last %s next %q err %v", ids(last), next, err)
	}
	if _, _, err := backend.List(ctx, "ada", mailbackend.Page{After: "junk"}); !errors.Is(err, mailbackend.ErrPage) {
		t.Fatalf("junk cursor %v", err)
	}
}
