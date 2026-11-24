// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

// Security is how a connection to the IMAP or SMTP server is protected.
type Security string

const (
	// SecurityTLS starts TLS before the first protocol byte (implicit TLS),
	// as on ports 993 and 465.
	SecurityTLS Security = "tls"
	// SecurityStartTLS connects in plaintext and upgrades with STARTTLS before
	// logging in, as on ports 143, 587, and 25. A server that does not offer
	// STARTTLS is an error. The connection never falls back to plaintext.
	SecurityStartTLS Security = "starttls"
	// SecurityNone sends everything, the password included, in plaintext.
	SecurityNone Security = "none"
)

// SettingError is a Config value that Open refuses before connecting.
type SettingError struct{ Msg string }

func (e *SettingError) Error() string { return "imap: " + e.Msg }

var (
	imapPorts = map[string]Security{"993": SecurityTLS, "143": SecurityStartTLS}
	smtpPorts = map[string]Security{"465": SecurityTLS, "587": SecurityStartTLS, "25": SecurityStartTLS}
)

// resolveSecurity returns the mode for addr. An empty setting is the default
// for a well-known port. SecurityNone to a host that is not loopback needs
// allowPlaintext, because the password would cross the network in plaintext.
func resolveSecurity(env, setting, addr string, ports map[string]Security, allowPlaintext bool) (Security, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", &SettingError{Msg: fmt.Sprintf("address %q is not host:port", addr)}
	}
	mode := Security(strings.ToLower(strings.TrimSpace(setting)))
	switch mode {
	case "":
		known, ok := ports[port]
		if !ok {
			return "", &SettingError{Msg: fmt.Sprintf("port %s has no default security; set %s to tls, starttls, or none", port, env)}
		}
		mode = known
	case SecurityTLS, SecurityStartTLS, SecurityNone:
	default:
		return "", &SettingError{Msg: fmt.Sprintf("%s is %q; use tls, starttls, or none", env, setting)}
	}
	if mode == SecurityNone && !loopback(host) && !allowPlaintext {
		return "", &SettingError{Msg: fmt.Sprintf("%s=none would send the mailbox password in plaintext to %s; none is allowed only for localhost, 127.0.0.0/8, and ::1 unless INBAI_ALLOW_PLAINTEXT_AUTH=1", env, host)}
	}
	return mode, nil
}

// loopback reports whether host is localhost, an address in 127.0.0.0/8, or
// ::1. net/smtp PlainAuth also trusts localhost, 127.0.0.1, and ::1 without
// TLS. This rule covers the rest of 127.0.0.0/8 too and applies to IMAP LOGIN.
func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// tlsConfig verifies the server certificate against the system roots for
// host, unless the operator turned verification off.
func (b *Backend) tlsConfig(host string) *tls.Config {
	cfg := &tls.Config{}
	if b.tlsBase != nil {
		cfg = b.tlsBase.Clone()
	}
	cfg.ServerName = host
	cfg.MinVersion = tls.VersionTLS12
	return cfg
}

// dialMode opens a TCP connection to addr and, for SecurityTLS, completes the
// TLS handshake before returning.
func (b *Backend) dialMode(ctx context.Context, addr string, mode Security) (net.Conn, string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, "", err
	}
	dial := b.dial
	if dial == nil {
		var dialer net.Dialer
		dial = dialer.DialContext
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil || mode != SecurityTLS {
		return conn, host, err
	}
	secure := tls.Client(conn, b.tlsConfig(host))
	if err := secure.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, "", err
	}
	return secure, host, nil
}

// openIMAP connects, reads the greeting, and runs STARTTLS when the mode
// asks for it. The returned connection is the one to close.
func (b *Backend) openIMAP(ctx context.Context) (net.Conn, *session, error) {
	conn, host, err := b.dialMode(ctx, b.imap, b.imapSecurity)
	if err != nil {
		return nil, nil, err
	}
	s := &session{r: bufio.NewReader(conn), w: conn}
	if err := s.greet(); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if b.imapSecurity != SecurityStartTLS {
		return conn, s, nil
	}
	secure, s, err := b.startTLS(ctx, conn, host, s)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return secure, s, nil
}

// startTLS runs the RFC 3501 STARTTLS command and returns a session over the
// TLS connection. Capabilities read before TLS are discarded and read again.
func (b *Backend) startTLS(ctx context.Context, conn net.Conn, host string, s *session) (net.Conn, *session, error) {
	caps, err := s.capabilities()
	if err != nil {
		return nil, nil, err
	}
	if !caps["STARTTLS"] {
		return nil, nil, fmt.Errorf("imap: %s does not offer STARTTLS; the password was not sent", b.imap)
	}
	if _, err := s.command("STARTTLS"); err != nil {
		return nil, nil, err
	}
	if s.r.Buffered() > 0 {
		return nil, nil, errors.New("imap: server sent data after STARTTLS before the TLS handshake")
	}
	secure := tls.Client(conn, b.tlsConfig(host))
	if err := secure.HandshakeContext(ctx); err != nil {
		return nil, nil, err
	}
	upgraded := &session{r: bufio.NewReader(secure), w: secure, seq: s.seq}
	caps, err = upgraded.capabilities()
	if err != nil {
		return nil, nil, err
	}
	if caps["LOGINDISABLED"] {
		return nil, nil, fmt.Errorf("imap: %s disables LOGIN after STARTTLS", b.imap)
	}
	return secure, upgraded, nil
}

// capabilities runs CAPABILITY and returns the upper-case capability names.
func (s *session) capabilities() (map[string]bool, error) {
	items, err := s.command("CAPABILITY")
	if err != nil {
		return nil, err
	}
	caps := map[string]bool{}
	for _, item := range items {
		fields := strings.Fields(item.text)
		if len(fields) < 2 || fields[0] != "*" || !strings.EqualFold(fields[1], "CAPABILITY") {
			continue
		}
		for _, name := range fields[2:] {
			caps[strings.ToUpper(name)] = true
		}
	}
	return caps, nil
}

// openSMTP connects, says EHLO, runs STARTTLS when the mode asks for it, and
// returns a client ready to authenticate.
func (b *Backend) openSMTP(ctx context.Context) (*smtp.Client, string, error) {
	conn, host, err := b.dialMode(ctx, b.smtp, b.smtpSecurity)
	if err != nil {
		return nil, "", err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, "", err
	}
	if err := client.Hello("inbai"); err != nil {
		_ = client.Close()
		return nil, "", err
	}
	if b.smtpSecurity == SecurityStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			_ = client.Close()
			return nil, "", fmt.Errorf("imap: smtp %s does not offer STARTTLS; the password was not sent", b.smtp)
		}
		if err := client.StartTLS(b.tlsConfig(host)); err != nil {
			_ = client.Close()
			return nil, "", err
		}
	}
	return client, host, nil
}

// smtpAuth is AUTH PLAIN. Over TLS it is net/smtp PlainAuth. With
// SecurityNone, Open has already applied the loopback rule, so it is a PLAIN
// exchange that does not repeat net/smtp's narrower localhost check.
func (b *Backend) smtpAuth(login Login, host string) smtp.Auth {
	if b.smtpSecurity == SecurityNone || b.smtpSecurity == "" {
		return plaintextAuth{user: login.User, password: login.Password}
	}
	return smtp.PlainAuth("", login.User, login.Password, host)
}

type plaintextAuth struct{ user, password string }

func (a plaintextAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.password), nil
}

func (a plaintextAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("imap: unexpected smtp auth challenge")
	}
	return nil, nil
}

// refused turns a 4xx or 5xx SMTP reply into a mailbackend.RefusedError, so
// the caller sees the mail server's reason. Other errors are returned as they
// are.
func refused(step string, err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) && reply.Code >= 400 && reply.Code < 600 {
		return fmt.Errorf("smtp %s: %w", step, &mailbackend.RefusedError{Type: fmt.Sprint(reply.Code), Description: reply.Msg})
	}
	return err
}
