// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package imap

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

// loginServer greets, then answers each command with reply. A reply of ""
// hangs up without answering.
func loginServer(t *testing.T, reply func(tag, cmd string) string) *Backend {
	t.Helper()
	addr := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		read := bufio.NewReader(conn)
		say(conn, "* OK ready\r\n")
		for {
			line, err := read.ReadString('\n')
			if err != nil {
				return
			}
			tag, cmd, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
			out := reply(tag, cmd)
			if out == "" {
				return
			}
			say(conn, "%s", out)
		}
	})
	return &Backend{imap: addr, accounts: map[string]Login{"alice": {User: "alice@example.com", Password: "hunter2-secret"}}}
}

func checkLoginError(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("no error")
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("error %q lacks %q", err, w)
		}
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error has the password: %q", err)
	}
	var refused *mailbackend.RefusedError
	if errors.As(err, &refused) {
		t.Fatalf("login failure must stay hidden from the caller: %v", err)
	}
}

func TestServerClosingDuringLoginNamesTheStepAndAccount(t *testing.T) {
	backend := loginServer(t, func(_, _ string) string { return "" })
	_, err := backend.MailState(context.Background(), "alice")
	checkLoginError(t, err, "closed the connection during LOGIN", `account "alice"`, "log in over IMAP")
}

func TestTaggedNoToLoginNamesTheAccountAndServerText(t *testing.T) {
	backend := loginServer(t, func(tag, _ string) string {
		return tag + " NO [AUTHENTICATIONFAILED] mailbox disabled by admin\r\n"
	})
	_, err := backend.MailState(context.Background(), "alice")
	checkLoginError(t, err, "LOGIN", `account "alice"`, "mailbox disabled by admin")
}

func TestServerClosingAfterLoginNamesTheStep(t *testing.T) {
	backend := loginServer(t, func(tag, cmd string) string {
		if strings.HasPrefix(cmd, "LOGIN") {
			return tag + " OK\r\n"
		}
		return ""
	})
	_, err := backend.MailState(context.Background(), "alice")
	if err == nil || !strings.Contains(err.Error(), "closed the connection during STATUS") {
		t.Fatalf("error %v", err)
	}
}
