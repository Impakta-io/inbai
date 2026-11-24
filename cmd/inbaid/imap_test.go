// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIMAPRetentionIsOffUnlessTheOperatorOptsIn(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		stored  string
		days    int
		deleted int
	}{
		{name: "default keeps mail", days: 0, deleted: 0},
		{name: "env opts in", env: "30", days: 30, deleted: 1},
		{name: "stored period opts in", stored: `{"days":14}`, days: 14, deleted: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stores atomic.Int32
			imapAddr := fakeIMAP(t, &stores)
			base := startIMAPServe(t, imapAddr, tc.env)
			if tc.stored != "" {
				call(t, http.MethodPut, base+"/v1/retention", tc.stored, nil)
			}
			var run struct {
				Deleted int `json:"deleted"`
			}
			call(t, http.MethodPost, base+"/v1/retention/run", "", &run)
			if run.Deleted != tc.deleted {
				t.Fatalf("deleted %d, want %d", run.Deleted, tc.deleted)
			}
			if tc.deleted == 0 && stores.Load() != 0 {
				t.Fatalf("flagged %d messages deleted", stores.Load())
			}
			var got struct {
				Days int `json:"days"`
			}
			call(t, http.MethodGet, base+"/v1/retention", "", &got)
			if got.Days != tc.days {
				t.Fatalf("reported %d days, want %d", got.Days, tc.days)
			}
		})
	}
}

func TestIMAPRetentionRejectsABadValue(t *testing.T) {
	for _, value := range []string{"-1", "thirty"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"serve"}, &stdout, &stderr, func(key string) string {
			switch key {
			case "INBAI_OPERATOR_TOKEN":
				return "0123456789abcdef0123456789abcdef0123"
			case "INBAI_MAIL":
				return "imap"
			case "INBAI_DOMAIN":
				return "example.com"
			case "INBAI_RETENTION_DAYS":
				return value
			default:
				return ""
			}
		})
		if code != 2 || !strings.Contains(stderr.String(), "INBAI_RETENTION_DAYS") {
			t.Fatalf("%q exit %d stderr %s", value, code, stderr.String())
		}
	}
}

// startIMAPServe runs inbaid serve in IMAP mode without a database. The server
// keeps running until the test binary exits, because serve has no shutdown.
func startIMAPServe(t *testing.T, imapAddr, retention string) string {
	t.Helper()
	accounts := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(accounts, []byte(`{"ada":{"user":"ada@example.com","password":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	env := map[string]string{
		"INBAI_OPERATOR_TOKEN": "0123456789abcdef0123456789abcdef0123",
		"INBAI_MAIL":           "imap",
		"INBAI_DOMAIN":         "example.com",
		"INBAI_IMAP_ADDRESS":   imapAddr,
		"INBAI_SMTP_ADDRESS":   imapAddr,
		"INBAI_IMAP_ACCOUNTS":  accounts,
		"INBAI_RETENTION_DAYS": retention,
	}
	go run([]string{"serve", "--addr", addr}, io.Discard, io.Discard, func(key string) string { return env[key] })
	base := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			return base
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func call(t *testing.T, method, url, body string, out any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef0123")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s: %d %s", method, url, resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: %v %s", method, url, err, raw)
		}
	}
}

// fakeIMAP serves one INBOX message received in 2020 and counts UID STORE commands.
func fakeIMAP(t *testing.T, stores *atomic.Int32) string {
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
			go func() {
				defer func() { _ = conn.Close() }()
				read := bufio.NewReader(conn)
				write := bufio.NewWriter(conn)
				say := func(format string, args ...any) { _, _ = fmt.Fprintf(write, format, args...) }
				say("* OK ready\r\n")
				_ = write.Flush()
				for {
					line, err := read.ReadString('\n')
					if err != nil {
						return
					}
					tag, cmd, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
					switch {
					case strings.HasPrefix(cmd, "UID STORE"):
						stores.Add(1)
						say("%s OK\r\n", tag)
					case strings.HasPrefix(cmd, "UID SEARCH"):
						say("* SEARCH 8\r\n%s OK\r\n", tag)
					case strings.HasPrefix(cmd, "UID FETCH"):
						header := "Subject: Old\r\n\r\n"
						say("* 1 FETCH (UID 8 INTERNALDATE \"01-Jan-2020 00:00:00 +0000\" BODY[HEADER.FIELDS (SUBJECT)] {%d}\r\n%s)\r\n%s OK\r\n", len(header), header, tag)
					case strings.HasPrefix(cmd, "UID EXPUNGE"):
						say("* 1 EXPUNGE\r\n%s OK\r\n", tag)
					case strings.HasPrefix(cmd, "LOGOUT"):
						say("* BYE\r\n%s OK\r\n", tag)
						_ = write.Flush()
						return
					default:
						say("%s OK\r\n", tag)
					}
					_ = write.Flush()
				}
			}()
		}
	}()
	return ln.Addr().String()
}
