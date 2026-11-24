// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

// testTLS returns a server config with the httptest certificate, which is
// valid for 127.0.0.1, and a pool that trusts it.
func testTLS(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.StartTLS()
	cert := srv.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	srv.Close()
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, roots
}

// record is a log of what a fake server saw, safe for the test to read.
type record struct {
	mu    sync.Mutex
	lines []string
}

func (r *record) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
}

func (r *record) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "|")
}

type fakeOptions struct {
	implicit bool   // TLS from the first byte
	starttls bool   // offer STARTTLS in plaintext
	rcpt     string // reply to RCPT, default 250
	mail     string // reply to MAIL, default 250
	done     string // reply after the DATA body, default 250
	auth     string // reply to AUTH, default 235
}

// fakeIMAPTLS is an IMAP server that answers CAPABILITY, STARTTLS, LOGIN,
// STATUS and LOGOUT. It records each command as "<plain|tls> <verb>".
func fakeIMAPTLS(t *testing.T, cfg *tls.Config, opts fakeOptions, seen *record) string {
	return listen(t, func(raw net.Conn) {
		var conn = raw
		defer func() { _ = conn.Close() }()
		secure := false
		if opts.implicit {
			conn = tls.Server(raw, cfg)
			secure = true
		}
		read := bufio.NewReader(conn)
		write := bufio.NewWriter(conn)
		say(write, "* OK ready\r\n")
		_ = write.Flush()
		for {
			line, err := read.ReadString('\n')
			if err != nil {
				return
			}
			tag, cmd, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
			verb, _, _ := strings.Cut(cmd, " ")
			mode := "plain"
			if secure {
				mode = "tls"
			}
			seen.add(mode + " " + verb)
			switch verb {
			case "CAPABILITY":
				caps := "IMAP4rev1"
				if !secure && opts.starttls {
					caps += " STARTTLS LOGINDISABLED"
				}
				say(write, "* CAPABILITY %s\r\n%s OK\r\n", caps, tag)
			case "STARTTLS":
				if secure || !opts.starttls {
					say(write, "%s BAD no\r\n", tag)
					break
				}
				say(write, "%s OK begin TLS\r\n", tag)
				_ = write.Flush()
				upgraded := tls.Server(raw, cfg)
				if err := upgraded.Handshake(); err != nil {
					return
				}
				conn, secure = upgraded, true
				read, write = bufio.NewReader(conn), bufio.NewWriter(conn)
				continue
			case "STATUS":
				say(write, "* STATUS INBOX (UIDVALIDITY 4 UIDNEXT 9)\r\n%s OK\r\n", tag)
			case "LOGOUT":
				say(write, "* BYE\r\n%s OK\r\n", tag)
				_ = write.Flush()
				return
			default:
				say(write, "%s OK\r\n", tag)
			}
			_ = write.Flush()
		}
	})
}

// fakeSMTPTLS is an SMTP server that records each command as
// "<plain|tls> <verb>".
func fakeSMTPTLS(t *testing.T, cfg *tls.Config, opts fakeOptions, seen *record) string {
	reply := func(custom, fallback string) string {
		if custom != "" {
			return custom + "\r\n"
		}
		return fallback + "\r\n"
	}
	return listen(t, func(raw net.Conn) {
		var conn = raw
		defer func() { _ = conn.Close() }()
		secure := false
		if opts.implicit {
			conn = tls.Server(raw, cfg)
			secure = true
		}
		read := bufio.NewReader(conn)
		write := bufio.NewWriter(conn)
		say(write, "220 ready\r\n")
		_ = write.Flush()
		for {
			line, err := read.ReadString('\n')
			if err != nil {
				return
			}
			verb, _, _ := strings.Cut(strings.ToUpper(strings.TrimRight(line, "\r\n")), " ")
			mode := "plain"
			if secure {
				mode = "tls"
			}
			seen.add(mode + " " + verb)
			switch verb {
			case "EHLO", "HELO":
				if !secure && opts.starttls {
					say(write, "250-localhost\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n")
				} else {
					say(write, "250-localhost\r\n250 AUTH PLAIN\r\n")
				}
			case "STARTTLS":
				say(write, "220 go ahead\r\n")
				_ = write.Flush()
				upgraded := tls.Server(raw, cfg)
				if err := upgraded.Handshake(); err != nil {
					return
				}
				conn, secure = upgraded, true
				read, write = bufio.NewReader(conn), bufio.NewWriter(conn)
				continue
			case "AUTH":
				say(write, "%s", reply(opts.auth, "235 ok"))
			case "MAIL":
				say(write, "%s", reply(opts.mail, "250 ok"))
			case "RCPT":
				say(write, "%s", reply(opts.rcpt, "250 ok"))
			case "DATA":
				say(write, "354 go\r\n")
				_ = write.Flush()
				for {
					part, err := read.ReadString('\n')
					if err != nil {
						return
					}
					if part == ".\r\n" {
						break
					}
				}
				say(write, "%s", reply(opts.done, "250 queued"))
			case "QUIT":
				say(write, "221 bye\r\n")
				_ = write.Flush()
				return
			default:
				say(write, "250 ok\r\n")
			}
			_ = write.Flush()
		}
	})
}

var ada = map[string]Login{"ada": {User: "ada@example.com", Password: "secret"}}

func outbound() mailbackend.Outbound {
	return mailbackend.Outbound{AccountID: "ada", From: "ada@example.com", To: "bob@example.com", Subject: "Hello", Body: "text"}
}

func TestIMAPStartTLSUpgradesBeforeLogin(t *testing.T) {
	cfg, roots := testTLS(t)
	var seen record
	addr := fakeIMAPTLS(t, cfg, fakeOptions{starttls: true}, &seen)
	backend := &Backend{imap: addr, imapSecurity: SecurityStartTLS, tlsBase: &tls.Config{RootCAs: roots}, accounts: ada}
	state, err := backend.MailState(context.Background(), "ada")
	if err != nil || state != "4:9" {
		t.Fatalf("state %q err %v", state, err)
	}
	got := seen.String()
	if !strings.HasPrefix(got, "plain CAPABILITY|plain STARTTLS|tls CAPABILITY|tls LOGIN|tls STATUS") || strings.Contains(got, "plain LOGIN") {
		t.Fatalf("commands %s", got)
	}
}

func TestIMAPStartTLSNotOfferedFails(t *testing.T) {
	cfg, roots := testTLS(t)
	var seen record
	addr := fakeIMAPTLS(t, cfg, fakeOptions{}, &seen)
	backend := &Backend{imap: addr, imapSecurity: SecurityStartTLS, tlsBase: &tls.Config{RootCAs: roots}, accounts: ada}
	_, err := backend.MailState(context.Background(), "ada")
	if err == nil || !strings.Contains(err.Error(), "does not offer STARTTLS") {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(seen.String(), "LOGIN") {
		t.Fatalf("sent LOGIN without TLS: %s", seen.String())
	}
}

func TestSMTPStartTLSOnSubmissionPort(t *testing.T) {
	cfg, roots := testTLS(t)
	var seen record
	addr := fakeSMTPTLS(t, cfg, fakeOptions{starttls: true}, &seen)
	backend := &Backend{smtp: addr, smtpSecurity: SecurityStartTLS, tlsBase: &tls.Config{RootCAs: roots}, accounts: ada}
	if err := backend.Deliver(context.Background(), outbound()); err != nil {
		t.Fatal(err)
	}
	got := seen.String()
	if !strings.HasPrefix(got, "plain EHLO|plain STARTTLS|tls EHLO|tls AUTH|tls MAIL|tls RCPT|tls DATA") {
		t.Fatalf("commands %s", got)
	}
}

func TestSMTPStartTLSNotOfferedFails(t *testing.T) {
	cfg, roots := testTLS(t)
	var seen record
	addr := fakeSMTPTLS(t, cfg, fakeOptions{}, &seen)
	backend := &Backend{smtp: addr, smtpSecurity: SecurityStartTLS, tlsBase: &tls.Config{RootCAs: roots}, accounts: ada}
	err := backend.Deliver(context.Background(), outbound())
	if err == nil || !strings.Contains(err.Error(), "does not offer STARTTLS") {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(seen.String(), "AUTH") || strings.Contains(seen.String(), "MAIL") {
		t.Fatalf("went on without TLS: %s", seen.String())
	}
}

func TestImplicitTLSVerifiesTheCertificate(t *testing.T) {
	cfg, roots := testTLS(t)
	var imapSeen, smtpSeen record
	imapAddr := fakeIMAPTLS(t, cfg, fakeOptions{implicit: true}, &imapSeen)
	smtpAddr := fakeSMTPTLS(t, cfg, fakeOptions{implicit: true}, &smtpSeen)
	trusted := &Backend{imap: imapAddr, smtp: smtpAddr, imapSecurity: SecurityTLS, smtpSecurity: SecurityTLS, tlsBase: &tls.Config{RootCAs: roots}, accounts: ada}
	if _, err := trusted.MailState(context.Background(), "ada"); err != nil {
		t.Fatal(err)
	}
	if err := trusted.Deliver(context.Background(), outbound()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(imapSeen.String(), "tls LOGIN") || !strings.Contains(smtpSeen.String(), "tls AUTH") {
		t.Fatalf("imap %s smtp %s", imapSeen.String(), smtpSeen.String())
	}
	system := &Backend{imap: imapAddr, smtp: smtpAddr, imapSecurity: SecurityTLS, smtpSecurity: SecurityTLS, accounts: ada}
	var unknown x509.UnknownAuthorityError
	if _, err := system.MailState(context.Background(), "ada"); !errors.As(err, &unknown) {
		t.Fatalf("imap accepted an untrusted certificate: %v", err)
	}
	if err := system.Deliver(context.Background(), outbound()); !errors.As(err, &unknown) {
		t.Fatalf("smtp accepted an untrusted certificate: %v", err)
	}
}

func accountFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`{"ada":{"user":"ada@example.com","password":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenChoosesSecurityByPortAndRefusesRemotePlaintext(t *testing.T) {
	accounts := accountFile(t)
	cases := []struct {
		cfg        Config
		imap, smtp Security
		refused    string
	}{
		{cfg: Config{IMAP: "mail.example.com:993", SMTP: "mail.example.com:587"}, imap: SecurityTLS, smtp: SecurityStartTLS},
		{cfg: Config{IMAP: "mail.example.com:143", SMTP: "mail.example.com:465"}, imap: SecurityStartTLS, smtp: SecurityTLS},
		{cfg: Config{IMAP: "mail.example.com:143", SMTP: "mail.example.com:25"}, imap: SecurityStartTLS, smtp: SecurityStartTLS},
		{cfg: Config{IMAP: "mail.example.com:3143", SMTP: "mail.example.com:587"}, refused: "INBAI_IMAP_SECURITY"},
		{cfg: Config{IMAP: "mail.example.com:993", SMTP: "mail.example.com:2525"}, refused: "INBAI_SMTP_SECURITY"},
		{cfg: Config{IMAP: "mail.example.com:3143", SMTP: "mail.example.com:2525", IMAPSecurity: "STARTTLS", SMTPSecurity: "tls"}, imap: SecurityStartTLS, smtp: SecurityTLS},
		{cfg: Config{IMAP: "mail.example.com:993", SMTP: "mail.example.com:587", IMAPSecurity: "ssl"}, refused: "use tls, starttls, or none"},
		{cfg: Config{IMAP: "mail.example.com:143", SMTP: "mail.example.com:587", IMAPSecurity: "none"}, refused: "INBAI_ALLOW_PLAINTEXT_AUTH"},
		{cfg: Config{IMAP: "mail.example.com:993", SMTP: "mail.example.com:587", SMTPSecurity: "none"}, refused: "INBAI_ALLOW_PLAINTEXT_AUTH"},
		{cfg: Config{IMAP: "10.0.0.5:143", SMTP: "10.0.0.5:25", IMAPSecurity: "none", SMTPSecurity: "none", AllowPlaintext: true}, imap: SecurityNone, smtp: SecurityNone},
		{cfg: Config{IMAP: "localhost:3143", SMTP: "127.0.0.2:3025", IMAPSecurity: "none", SMTPSecurity: "none"}, imap: SecurityNone, smtp: SecurityNone},
		{cfg: Config{IMAP: "[::1]:143", SMTP: "127.0.0.1:25", IMAPSecurity: "none", SMTPSecurity: "none"}, imap: SecurityNone, smtp: SecurityNone},
	}
	for _, tc := range cases {
		tc.cfg.Accounts = accounts
		backend, err := Open(tc.cfg)
		if tc.refused != "" {
			var setting *SettingError
			if !errors.As(err, &setting) || !strings.Contains(err.Error(), tc.refused) {
				t.Errorf("%+v: err %v, want a SettingError naming %s", tc.cfg, err, tc.refused)
			}
			continue
		}
		if err != nil {
			t.Errorf("%+v: %v", tc.cfg, err)
			continue
		}
		if imapMode, smtpMode := backend.Security(); imapMode != tc.imap || smtpMode != tc.smtp {
			t.Errorf("%+v: imap %s smtp %s, want %s %s", tc.cfg, imapMode, smtpMode, tc.imap, tc.smtp)
		}
	}
}

func TestPlaintextOptInSendsToARemoteHost(t *testing.T) {
	var seen record
	local := fakeSMTPTLS(t, nil, fakeOptions{}, &seen)
	backend, err := Open(Config{IMAP: "mail.example.com:993", SMTP: "mail.example.com:2525", SMTPSecurity: "none", AllowPlaintext: true, Accounts: accountFile(t)})
	if err != nil {
		t.Fatal(err)
	}
	backend.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, local)
	}
	// net/smtp PlainAuth refuses an unencrypted connection to any host but
	// localhost. The operator opted in, so the backend sends AUTH PLAIN.
	if err := backend.Deliver(context.Background(), outbound()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen.String(), "plain AUTH") {
		t.Fatalf("commands %s", seen.String())
	}
}

func TestSMTPRefusalIsARefusedError(t *testing.T) {
	cases := []struct {
		opts       fakeOptions
		code, text string
		notRefusal bool
	}{
		{opts: fakeOptions{rcpt: "550 5.1.1 <bob@example.com>: no such user"}, code: "550", text: "5.1.1 <bob@example.com>: no such user"},
		{opts: fakeOptions{mail: "451 4.7.1 try again later"}, code: "451", text: "4.7.1 try again later"},
		{opts: fakeOptions{done: "554 5.7.1 message rejected as spam"}, code: "554", text: "5.7.1 message rejected as spam"},
		{opts: fakeOptions{auth: "535 5.7.8 authentication failed"}, notRefusal: true},
	}
	for _, tc := range cases {
		var seen record
		addr := fakeSMTPTLS(t, nil, tc.opts, &seen)
		backend := &Backend{smtp: addr, accounts: ada}
		err := backend.Deliver(context.Background(), outbound())
		if err == nil {
			t.Fatalf("%+v: delivered", tc.opts)
		}
		var refusal *mailbackend.RefusedError
		if tc.notRefusal {
			if errors.As(err, &refusal) {
				t.Errorf("%+v: an auth failure is shown to the caller: %v", tc.opts, err)
			}
			continue
		}
		if !errors.As(err, &refusal) || refusal.Type != tc.code || refusal.Description != tc.text {
			t.Errorf("%+v: err %v", tc.opts, err)
		}
	}
}
