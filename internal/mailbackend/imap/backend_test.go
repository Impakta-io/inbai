// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package imap

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
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
		for {
			line, err := read.ReadString('\n')
			if err != nil {
				return
			}
			tag, cmd, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
			switch {
			case strings.HasPrefix(cmd, "LOGIN"), strings.HasPrefix(cmd, "SELECT"), strings.HasPrefix(cmd, "UID STORE"):
				say(write, "%s OK\r\n", tag)
			case cmd == "UID SEARCH ALL" || strings.HasPrefix(cmd, "UID SEARCH TEXT"):
				say(write, "* SEARCH 8\r\n%s OK\r\n", tag)
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
	if err != nil || message.Text != "body" {
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

func TestUIDValidityChangeDoesNotAdvance(t *testing.T) {
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
	if _, err := backend.MailChanges(context.Background(), "ada", "4:8"); err == nil {
		t.Fatal("accepted a new uidvalidity")
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

func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
