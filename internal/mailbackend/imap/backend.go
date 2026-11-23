// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

// Login is the mailbox password for one account that already exists.
type Login struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// Config selects the IMAP and SMTP servers and the account file.
// The account file is a JSON object of account id to user and password.
type Config struct {
	IMAP     string
	SMTP     string
	TLS      bool
	Accounts string
}

// Backend reads and submits mail for accounts named in the account file.
type Backend struct {
	imap     string
	smtp     string
	tls      bool
	accounts map[string]Login
	dial     func(context.Context, string, string) (net.Conn, error)
}

// Open reads the account file and returns a backend. It does not connect yet.
func Open(cfg Config) (*Backend, error) {
	if cfg.IMAP == "" || cfg.SMTP == "" || cfg.Accounts == "" {
		return nil, errors.New("imap: address and account file are required")
	}
	raw, err := os.ReadFile(cfg.Accounts)
	if err != nil {
		return nil, err
	}
	var accounts map[string]Login
	if err := json.Unmarshal(raw, &accounts); err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, errors.New("imap: account file is empty")
	}
	return &Backend{imap: cfg.IMAP, smtp: cfg.SMTP, tls: cfg.TLS, accounts: accounts}, nil
}

// CreateInbox reports that this backend cannot provision a mailbox.
func (b *Backend) CreateInbox(context.Context, mailbackend.InboxSpec) (string, error) {
	return "", errors.New("imap: this backend does not provision accounts")
}

// CreateInboxes reports that this backend cannot provision mailboxes.
func (b *Backend) CreateInboxes(context.Context, []mailbackend.InboxSpec) ([]string, error) {
	return nil, errors.New("imap: this backend does not provision accounts")
}

// DeleteInbox reports that this backend cannot remove an account.
func (b *Backend) DeleteInbox(context.Context, string) error {
	return errors.New("imap: this backend does not delete accounts")
}

// ListInboxes returns the accounts named in the account file.
func (b *Backend) ListInboxes(_ context.Context, page mailbackend.Page) ([]mailbackend.Account, string, error) {
	out := make([]mailbackend.Account, 0, len(b.accounts))
	for id, login := range b.accounts {
		local := login.User
		if at := strings.IndexByte(local, '@'); at > 0 {
			local = local[:at]
		}
		out = append(out, mailbackend.Account{ID: id, Local: local})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return mailbackend.Slice(out, func(account mailbackend.Account) string { return account.ID }, page)
}

// Download reports that IMAP messages are read with Get. There is no blob id.
func (b *Backend) Download(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("imap: this backend has no blob download")
}

// Deliver submits one message over SMTP using the account password.
func (b *Backend) Deliver(ctx context.Context, msg mailbackend.Outbound) error {
	login, err := b.login(msg.AccountID)
	if err != nil {
		return err
	}
	conn, err := b.connect(ctx, b.smtp)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	host, _, err := net.SplitHostPort(b.smtp)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := client.Hello("inbai"); err != nil {
		return err
	}
	if err := client.Auth(smtp.PlainAuth("", login.User, login.Password, host)); err != nil {
		return err
	}
	from := msg.From
	if from == "" {
		from = login.User
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(msg.To); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(messageBytes(msg, from)); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

// List returns the inbox messages with their subjects.
func (b *Backend) List(ctx context.Context, account string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	return b.fetchSearch(ctx, account, "ALL", page)
}

// Search returns messages whose text matches the query.
func (b *Backend) Search(ctx context.Context, account, query string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	return b.fetchSearch(ctx, account, "TEXT "+quote(query), page)
}

// Get reads one message body. The id is the IMAP UID.
func (b *Backend) Get(ctx context.Context, account, messageID string) (mailbackend.Message, error) {
	uid, err := strconv.Atoi(messageID)
	if err != nil {
		return mailbackend.Message{}, fmt.Errorf("imap: message id is a uid")
	}
	var message mailbackend.Message
	err = b.with(ctx, account, func(s *session) error {
		if _, err := s.command("SELECT", "INBOX"); err != nil {
			return err
		}
		items, err := s.command("UID", "FETCH", messageID, "(UID BODY.PEEK[TEXT])")
		if err != nil {
			return err
		}
		got, ok := fetched(items)[uid]
		if !ok || len(got.literals) == 0 {
			return fmt.Errorf("imap: message %s was not found", messageID)
		}
		message = mailbackend.Message{ID: messageID, Text: string(got.literals[0])}
		return nil
	})
	return message, err
}

// Delete marks one UID deleted and expunges it.
func (b *Backend) Delete(ctx context.Context, account, messageID string) error {
	if _, err := strconv.Atoi(messageID); err != nil {
		return fmt.Errorf("imap: message id is a uid")
	}
	return b.with(ctx, account, func(s *session) error {
		if _, err := s.command("SELECT", "INBOX"); err != nil {
			return err
		}
		if _, err := s.command("UID", "STORE", messageID, "+FLAGS.SILENT", `(\Deleted)`); err != nil {
			return err
		}
		_, err := s.command("UID", "EXPUNGE", messageID)
		return err
	})
}

// MailState returns uidvalidity and uidnext. Deletes are not part of this cursor.
func (b *Backend) MailState(ctx context.Context, account string) (string, error) {
	var state string
	err := b.with(ctx, account, func(s *session) error {
		items, err := s.command("STATUS", "INBOX", "(UIDVALIDITY UIDNEXT)")
		if err != nil {
			return err
		}
		validity, next, err := statusOf(items)
		if err != nil {
			return err
		}
		state = formatState(validity, next)
		return nil
	})
	return state, err
}

// MailChanges reports UIDs added since the stored uidnext.
// A changed uidvalidity is an error, and the caller keeps the old cursor.
// Messages removed since the cursor are not reported.
func (b *Backend) MailChanges(ctx context.Context, account, since string) (mailbackend.Delta, error) {
	oldValidity, oldNext, err := parseState(since)
	if err != nil {
		return mailbackend.Delta{}, err
	}
	var delta mailbackend.Delta
	err = b.with(ctx, account, func(s *session) error {
		items, err := s.command("STATUS", "INBOX", "(UIDVALIDITY UIDNEXT)")
		if err != nil {
			return err
		}
		validity, next, err := statusOf(items)
		if err != nil {
			return err
		}
		if validity != oldValidity {
			return errors.New("imap: mailbox uidvalidity changed")
		}
		delta.State = formatState(validity, next)
		if next <= oldNext {
			return nil
		}
		items, err = s.command("UID", "SEARCH", "UID", fmt.Sprintf("%d:%d", oldNext, next-1))
		if err != nil {
			return err
		}
		for _, uid := range searchUIDs(items) {
			delta.Created = append(delta.Created, strconv.Itoa(uid))
		}
		return nil
	})
	return delta, err
}

func (b *Backend) fetchSearch(ctx context.Context, account, criteria string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	var messages []mailbackend.Message
	var next string
	err := b.with(ctx, account, func(s *session) error {
		if _, err := s.command("SELECT", "INBOX"); err != nil {
			return err
		}
		items, err := s.command("UID", "SEARCH", criteria)
		if err != nil {
			return err
		}
		uids := searchUIDs(items)
		ids := make([]string, len(uids))
		for i, uid := range uids {
			ids[i] = strconv.Itoa(uid)
		}
		kept, cursor, err := mailbackend.Slice(ids, func(id string) string { return id }, page)
		if err != nil {
			return err
		}
		next = cursor
		if len(kept) == 0 {
			messages = []mailbackend.Message{}
			return nil
		}
		items, err = s.command("UID", "FETCH", strings.Join(kept, ","), "(UID INTERNALDATE BODY.PEEK[HEADER.FIELDS (SUBJECT)])")
		if err != nil {
			return err
		}
		got := fetched(items)
		messages = make([]mailbackend.Message, 0, len(kept))
		for _, id := range kept {
			uid, _ := strconv.Atoi(id)
			item := got[uid]
			var header []byte
			if len(item.literals) > 0 {
				header = item.literals[0]
			}
			messages = append(messages, mailbackend.Message{ID: id, Subject: subjectOf(header), ReceivedAt: internalDate(item.text)})
		}
		return nil
	})
	return messages, next, err
}

func (b *Backend) with(ctx context.Context, account string, fn func(*session) error) error {
	login, err := b.login(account)
	if err != nil {
		return err
	}
	conn, err := b.connect(ctx, b.imap)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	s := &session{r: bufio.NewReader(conn), w: conn}
	if err := s.greet(); err != nil {
		return err
	}
	if _, err := s.command("LOGIN", quote(login.User), quote(login.Password)); err != nil {
		return err
	}
	defer func() { _, _ = s.command("LOGOUT") }()
	return fn(s)
}

func (b *Backend) login(account string) (Login, error) {
	login, ok := b.accounts[account]
	if !ok || login.User == "" {
		return Login{}, fmt.Errorf("imap: unknown account %s", account)
	}
	return login, nil
}

func (b *Backend) connect(ctx context.Context, addr string) (net.Conn, error) {
	if b.dial != nil {
		return b.dial(ctx, "tcp", addr)
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil || !b.tls {
		return conn, err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	secure := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err := secure.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return secure, nil
}

func messageBytes(msg mailbackend.Outbound, from string) []byte {
	var body strings.Builder
	writeHeader := func(name, value string) {
		body.WriteString(name)
		body.WriteString(": ")
		body.WriteString(strings.NewReplacer("\r", "", "\n", "").Replace(value))
		body.WriteString("\r\n")
	}
	writeHeader("From", from)
	writeHeader("To", msg.To)
	writeHeader("Subject", msg.Subject)
	writeHeader("Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader("Message-ID", fmt.Sprintf("<inbai.%d@localhost>", time.Now().UnixNano()))
	if len(msg.InReplyTo) > 0 {
		writeHeader("In-Reply-To", strings.Join(msg.InReplyTo, " "))
	}
	if len(msg.References) > 0 {
		writeHeader("References", strings.Join(msg.References, " "))
	}
	writeHeader("MIME-Version", "1.0")
	if len(msg.Attachments) == 0 {
		writeHeader("Content-Type", "text/plain; charset=utf-8")
		body.WriteString("\r\n")
		body.WriteString(strings.ReplaceAll(msg.Body, "\n", "\r\n"))
		body.WriteString("\r\n")
		return []byte(body.String())
	}
	boundary := "inbai-boundary"
	writeHeader("Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	body.WriteString("\r\n")
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	body.WriteString(strings.ReplaceAll(msg.Body, "\n", "\r\n"))
	body.WriteString("\r\n")
	for _, file := range msg.Attachments {
		body.WriteString("--" + boundary + "\r\n")
		kind := file.Type
		if kind == "" {
			kind = "application/octet-stream"
		}
		body.WriteString("Content-Type: " + kind + "\r\n")
		body.WriteString("Content-Transfer-Encoding: base64\r\n")
		body.WriteString("Content-Disposition: attachment; filename=\"" + strings.NewReplacer("\r", "", "\n", "", `"`, "").Replace(file.Name) + "\"\r\n\r\n")
		encoded := base64.StdEncoding.EncodeToString(file.Data)
		for len(encoded) > 76 {
			body.WriteString(encoded[:76])
			body.WriteString("\r\n")
			encoded = encoded[76:]
		}
		body.WriteString(encoded)
		body.WriteString("\r\n")
	}
	body.WriteString("--" + boundary + "--\r\n")
	return []byte(body.String())
}
