// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package imap

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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
//
// IMAPSecurity and SMTPSecurity are tls, starttls, or none. Empty is the
// default for the port: 993 and 465 are tls, 143, 587, and 25 are starttls.
// Another port needs an explicit value. none is refused for a host that is
// not loopback unless AllowPlaintext is set. InsecureTLS skips certificate
// verification.
type Config struct {
	IMAP           string
	SMTP           string
	IMAPSecurity   string
	SMTPSecurity   string
	AllowPlaintext bool
	InsecureTLS    bool
	Accounts       string
	// AuthservID is the authserv-id the mail server writes in the
	// Authentication-Results fields it adds, usually its hostname. Only that
	// server's results are reported. When it is empty, none are.
	AuthservID string
}

// Backend reads and submits mail for accounts named in the account file.
// Open sets both security modes. An empty mode, which only a Backend built
// in this package's tests has, is plaintext.
type Backend struct {
	imap         string
	smtp         string
	imapSecurity Security
	smtpSecurity Security
	tlsBase      *tls.Config
	accounts     map[string]Login
	dial         func(context.Context, string, string) (net.Conn, error)

	authservID string
}

// Open reads the account file and returns a backend. It does not connect yet.
func Open(cfg Config) (*Backend, error) {
	if cfg.IMAP == "" || cfg.SMTP == "" || cfg.Accounts == "" {
		return nil, errors.New("imap: address and account file are required")
	}
	imapSecurity, err := resolveSecurity("INBAI_IMAP_SECURITY", cfg.IMAPSecurity, cfg.IMAP, imapPorts, cfg.AllowPlaintext)
	if err != nil {
		return nil, err
	}
	smtpSecurity, err := resolveSecurity("INBAI_SMTP_SECURITY", cfg.SMTPSecurity, cfg.SMTP, smtpPorts, cfg.AllowPlaintext)
	if err != nil {
		return nil, err
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
	return &Backend{
		imap:         cfg.IMAP,
		smtp:         cfg.SMTP,
		imapSecurity: imapSecurity,
		smtpSecurity: smtpSecurity,
		tlsBase:      &tls.Config{InsecureSkipVerify: cfg.InsecureTLS}, //nolint:gosec // only when the operator sets INBAI_IMAP_TLS_INSECURE=1
		accounts:     accounts,
		authservID:   strings.TrimSpace(cfg.AuthservID),
	}, nil
}

// Security returns the IMAP and SMTP modes Open chose.
func (b *Backend) Security() (imapMode, smtpMode Security) {
	return b.imapSecurity, b.smtpSecurity
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

// SenderAddresses returns the account's login user from the account file.
// This backend has no alias list, so that is the only address the account sends as.
func (b *Backend) SenderAddresses(_ context.Context, account string) ([]string, error) {
	login, err := b.login(account)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(login.User, "@") {
		return nil, fmt.Errorf("imap: account %s logs in as %q, which is not an email address", account, login.User)
	}
	return []string{login.User}, nil
}

// Download reports that IMAP messages are read with Get. There is no blob id.
func (b *Backend) Download(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("imap: this backend has no blob download")
}

// Deliver submits one message over SMTP using the account password.
func (b *Backend) Deliver(ctx context.Context, msg mailbackend.Outbound) error {
	for _, file := range msg.Attachments {
		if err := noLineBreaks("attachment type", file.Type); err != nil {
			return err
		}
	}
	login, err := b.login(msg.AccountID)
	if err != nil {
		return err
	}
	client, host, err := b.openSMTP(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := client.Auth(b.smtpAuth(login, host)); err != nil {
		return err
	}
	from := msg.From
	if from == "" {
		from = login.User
	}
	if err := client.Mail(from); err != nil {
		return refused("MAIL", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return refused("RCPT", err)
	}
	writer, err := client.Data()
	if err != nil {
		return refused("DATA", err)
	}
	if _, err := writer.Write(messageBytes(msg, from)); err != nil {
		_ = writer.Close()
		return err
	}
	return refused("DATA", writer.Close())
}

// List returns the inbox messages with their subjects, newest first.
func (b *Backend) List(ctx context.Context, account string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	return b.fetchSearch(ctx, account, "ALL", page)
}

// Search returns messages whose text matches the query.
func (b *Backend) Search(ctx context.Context, account, query string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	quoted, err := quote(query)
	if err != nil {
		return nil, "", fmt.Errorf("search query: %w", err)
	}
	return b.fetchSearch(ctx, account, "TEXT "+quoted, page)
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
		text := string(got.literals[0])
		authItems, err := s.command("UID", "FETCH", messageID, "(UID FLAGS BODY.PEEK[HEADER.FIELDS (AUTHENTICATION-RESULTS FROM REPLY-TO)])")
		if err != nil {
			return err
		}
		auth := fetched(authItems)[uid]
		header := ""
		if len(auth.literals) > 0 {
			header = string(auth.literals[0])
		}
		message = mailbackend.Message{
			ID:             messageID,
			Text:           text,
			From:           headerField(header, "From"),
			ReplyTo:        headerField(header, "Reply-To"),
			Authentication: mailbackend.AuthenticationOf(headerFields(header, "Authentication-Results"), b.authservID, isJunk(flagsOf(auth.text))),
		}
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
// A changed uidvalidity is mailbackend.ErrChangesLost: the old uids mean nothing now.
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
			return fmt.Errorf("imap: mailbox uidvalidity changed: %w", mailbackend.ErrChangesLost)
		}
		delta.State = formatState(validity, next)
		if next <= oldNext {
			return nil
		}
		if _, err := s.command("EXAMINE", "INBOX"); err != nil {
			return err
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
		kept, cursor, err := newestFirst(searchUIDs(items), page)
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

// newestFirst orders UIDs from newest to oldest and returns the page that
// follows the cursor. The cursor is the last UID returned. UIDs only grow, so a
// message that arrives between pages is newer than every cursor and cannot
// shift or repeat a page. A cursor UID that has since been deleted still works.
func newestFirst(uids []int, page mailbackend.Page) ([]string, string, error) {
	limit, err := page.Bound()
	if err != nil {
		return nil, "", err
	}
	before := -1
	if page.After != "" {
		before, err = strconv.Atoi(page.After)
		if err != nil || before < 1 {
			return nil, "", mailbackend.ErrPage
		}
	}
	sorted := append([]int(nil), uids...)
	sort.Sort(sort.Reverse(sort.IntSlice(sorted)))
	var ids []string
	more := false
	for _, uid := range sorted {
		if before >= 0 && uid >= before {
			continue
		}
		if len(ids) == limit {
			more = true
			break
		}
		ids = append(ids, strconv.Itoa(uid))
	}
	next := ""
	if more {
		next = ids[len(ids)-1]
	}
	return ids, next, nil
}

func (b *Backend) with(ctx context.Context, account string, fn func(*session) error) error {
	login, err := b.login(account)
	if err != nil {
		return err
	}
	conn, s, err := b.openIMAP(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	user, err := quote(login.User)
	if err != nil {
		return err
	}
	password, err := quote(login.Password)
	if err != nil {
		return err
	}
	if _, err := s.command("LOGIN", user, password); err != nil {
		return loginError(account, err)
	}
	defer func() { _, _ = s.command("LOGOUT") }()
	return fn(s)
}

// loginError says which account and step failed. The text goes to the server
// log and is not shown to the API caller. It never includes the password.
func loginError(account string, err error) error {
	var closed *closedError
	var status *statusError
	switch {
	case errors.As(err, &closed):
		return fmt.Errorf("imap: server closed the connection during LOGIN for account %q (check that this mailbox user may log in over IMAP)", account)
	case errors.As(err, &status):
		return fmt.Errorf("imap: server refused LOGIN for account %q: %s", account, strings.TrimSpace(status.text))
	}
	return fmt.Errorf("imap: LOGIN for account %q: %w", account, err)
}

func headerField(header, name string) string {
	prefix := strings.ToLower(name) + ":"
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(name)+1:])
		}
	}
	return ""
}

// headerFields returns the value of every name field in header, in header
// order, with folded lines joined.
func headerFields(header, name string) []string {
	prefix := strings.ToLower(name) + ":"
	var out []string
	current := -1
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" && (line[0] == ' ' || line[0] == '\t') {
			if current >= 0 {
				out[current] += " " + strings.TrimSpace(line)
			}
			continue
		}
		current = -1
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			out = append(out, strings.TrimSpace(line[len(prefix):]))
			current = len(out) - 1
		}
	}
	return out
}

func (b *Backend) login(account string) (Login, error) {
	login, ok := b.accounts[account]
	if !ok || login.User == "" {
		return Login{}, fmt.Errorf("imap: unknown account %s", account)
	}
	return login, nil
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
