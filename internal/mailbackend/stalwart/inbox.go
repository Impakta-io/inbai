// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

// InboxInput names the account to create inside an existing domain.
type InboxInput struct {
	DomainID   string
	RoleID     string
	Local      string
	QuotaBytes int64
}

// Delivery is one message submitted as an existing account.
type Delivery struct {
	AccountID   string
	From        string
	To          string
	Subject     string
	Body        string
	InReplyTo   []string
	References  []string
	Attachments []mailbackend.File
}

// Message is one message in an inbox.
type Message struct {
	ID             string                     `json:"id"`
	Subject        string                     `json:"subject"`
	Preview        string                     `json:"preview"`
	Text           string                     `json:"text,omitempty"`
	TextTruncated  bool                       `json:"textTruncated,omitempty"`
	MessageID      []string                   `json:"messageId,omitempty"`
	InReplyTo      []string                   `json:"inReplyTo,omitempty"`
	References     []string                   `json:"references,omitempty"`
	ReceivedAt     time.Time                  `json:"receivedAt,omitempty"`
	From           string                     `json:"from,omitempty"`
	ReplyTo        string                     `json:"replyTo,omitempty"`
	Attachments    []Attachment               `json:"attachments,omitempty"`
	Authentication mailbackend.Authentication `json:"authentication,omitempty"`
}

// Attachment is one file on a stored message.
type Attachment struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int    `json:"size"`
	BlobID string `json:"blobId,omitempty"`
}

// CreateInbox creates a Stalwart account that can receive mail.
func CreateInbox(ctx context.Context, client *Client, in InboxInput) (string, error) {
	if client == nil {
		return "", fmt.Errorf("missing stalwart client")
	}
	local := strings.ToLower(strings.TrimSpace(in.Local))
	if local == "" || strings.ContainsAny(local, " @/\t") {
		return "", fmt.Errorf("local part %q is not an address", in.Local)
	}
	if in.DomainID == "" || in.RoleID == "" {
		return "", fmt.Errorf("inbox needs a domain and a role")
	}
	secret, err := randomSecret()
	if err != nil {
		return "", err
	}
	raw, err := client.invoke(ctx, "a", "x:Account/set", map[string]any{
		"create": map[string]any{
			"a": accountWithQuota(local, in.DomainID, in.RoleID, secret, in.QuotaBytes),
		},
	})
	if err != nil {
		return "", err
	}
	return createdID(raw, "a")
}

// Deliver submits a message as the account. Stalwart delivers it.
func Deliver(ctx context.Context, client *Client, msg Delivery) error {
	if client == nil {
		return fmt.Errorf("missing stalwart client")
	}
	if msg.AccountID == "" || msg.From == "" || msg.To == "" {
		return fmt.Errorf("delivery needs an account, a sender, and a recipient")
	}
	identityID, err := identityFor(ctx, client, msg.AccountID, msg.From)
	if err != nil {
		return err
	}
	roles, err := mailboxesByRole(ctx, client, msg.AccountID)
	if err != nil {
		return err
	}
	targetBox := roles["sent"]
	if targetBox == "" {
		targetBox = roles["drafts"]
	}
	if targetBox == "" {
		targetBox = roles["inbox"]
	}
	if targetBox == "" {
		return fmt.Errorf("account %s has no mailbox", msg.AccountID)
	}
	outbound := map[string]any{
		"mailboxIds": map[string]bool{targetBox: true},
		"from":       []any{map[string]any{"email": msg.From}},
		"to":         []any{map[string]any{"email": msg.To}},
		"subject":    msg.Subject,
		"keywords":   map[string]bool{"$seen": true},
		"textBody":   []any{map[string]any{"partId": "p", "type": "text/plain"}},
		"bodyValues": map[string]any{"p": map[string]any{"value": msg.Body}},
	}
	if len(msg.InReplyTo) > 0 {
		outbound["inReplyTo"] = msg.InReplyTo
	}
	if len(msg.References) > 0 {
		outbound["references"] = msg.References
	}
	if len(msg.Attachments) > 0 {
		files := make([]any, 0, len(msg.Attachments))
		for _, file := range msg.Attachments {
			blobID, err := client.upload(ctx, msg.AccountID, file.Type, file.Data)
			if err != nil {
				return err
			}
			kind := file.Type
			if kind == "" {
				kind = "application/octet-stream"
			}
			files = append(files, map[string]any{
				"blobId":      blobID,
				"name":        file.Name,
				"type":        kind,
				"disposition": "attachment",
			})
		}
		outbound["attachments"] = files
	}
	found, err := client.invokeMany(ctx, []jmapCall{
		{Name: "Email/set", ID: "e", Args: map[string]any{
			"accountId": msg.AccountID,
			"create":    map[string]any{"m": outbound},
		}},
		{Name: "EmailSubmission/set", ID: "s", Args: map[string]any{
			"accountId": msg.AccountID,
			"create": map[string]any{
				"s": map[string]any{
					"emailId":    "#m",
					"identityId": identityID,
					"envelope": map[string]any{
						"mailFrom": map[string]any{"email": msg.From},
						"rcptTo":   []any{map[string]any{"email": msg.To}},
					},
				},
			},
		}},
	})
	if err != nil {
		return err
	}
	emailID, err := createdOnly(found["e"], "m")
	if err != nil {
		return fmt.Errorf("stalwart did not store the message: %w", err)
	}
	if _, err := createdOnly(found["s"], "s"); err != nil {
		err = fmt.Errorf("stalwart did not submit the message: %w", err)
		if cleanup := DeleteMessage(ctx, client, msg.AccountID, emailID); cleanup != nil {
			return fmt.Errorf("%w (stored copy %s was not removed: %v)", err, emailID, cleanup)
		}
		return err
	}
	return nil
}

// SenderAddresses returns the email of each JMAP Identity on the account, in the order Stalwart lists them.
func SenderAddresses(ctx context.Context, client *Client, accountID string) ([]string, error) {
	if client == nil {
		return nil, fmt.Errorf("missing stalwart client")
	}
	if accountID == "" {
		return nil, fmt.Errorf("sender addresses need an account")
	}
	found, err := identities(ctx, client, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(found))
	for _, one := range found {
		out = append(out, one.Email)
	}
	return out, nil
}

type identity struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// identities returns the account's JMAP Identities that have both an id and an email.
func identities(ctx context.Context, client *Client, accountID string) ([]identity, error) {
	raw, err := client.invoke(ctx, "i", "Identity/get", map[string]any{
		"accountId":  accountID,
		"ids":        nil,
		"properties": []string{"id", "email"},
	})
	if err != nil {
		return nil, err
	}
	var got struct {
		List []identity `json:"list"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, err
	}
	out := make([]identity, 0, len(got.List))
	for _, one := range got.List {
		if one.ID != "" && one.Email != "" {
			out = append(out, one)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("account %s has no identity", accountID)
	}
	return out, nil
}

// identityFor returns the id of the Identity whose email is from, compared case-insensitively.
func identityFor(ctx context.Context, client *Client, accountID, from string) (string, error) {
	found, err := identities(ctx, client, accountID)
	if err != nil {
		return "", err
	}
	for _, one := range found {
		if strings.EqualFold(one.Email, from) {
			return one.ID, nil
		}
	}
	return "", fmt.Errorf("account %s has no identity for %s", accountID, from)
}

// createdOnly returns the id Stalwart created for key. Unlike createdID, it
// treats every notCreated entry as a failure, so an existing object is never
// mistaken for one this call made. A notCreated entry is a
// mailbackend.RefusedError carrying the SetError type and description.
func createdOnly(raw json.RawMessage, key string) (string, error) {
	if raw == nil {
		return "", fmt.Errorf("stalwart %s: missing response", key)
	}
	var body setResult
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", err
	}
	if missing, ok := body.NotCreated[key]; ok {
		return "", fmt.Errorf("stalwart %s: %w", key, &mailbackend.RefusedError{Type: missing.Type, Description: missing.Description})
	}
	created, ok := body.Created[key]
	if !ok {
		return "", fmt.Errorf("stalwart %s: missing result", key)
	}
	if created.ID == "" {
		return "", fmt.Errorf("stalwart %s: missing id", key)
	}
	return created.ID, nil
}

func mailboxesByRole(ctx context.Context, client *Client, accountID string) (map[string]string, error) {
	raw, err := client.invoke(ctx, "m", "Mailbox/get", map[string]any{
		"accountId":  accountID,
		"ids":        nil,
		"properties": []string{"id", "role"},
	})
	if err != nil {
		return nil, err
	}
	var got struct {
		List []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"list"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, err
	}
	roles := make(map[string]string, len(got.List))
	for _, box := range got.List {
		if box.Role != "" && box.ID != "" {
			roles[strings.ToLower(box.Role)] = box.ID
		}
	}
	return roles, nil
}

func inboxMailboxID(ctx context.Context, client *Client, accountID string) (string, error) {
	raw, err := client.invoke(ctx, "q", "Mailbox/query", map[string]any{
		"accountId": accountID,
		"filter":    map[string]any{"role": "inbox"},
	})
	if err != nil {
		return "", err
	}
	var query struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(raw, &query); err != nil {
		return "", err
	}
	if len(query.IDs) == 0 {
		return "", fmt.Errorf("account %s has no inbox", accountID)
	}
	return query.IDs[0], nil
}

// ListMessages returns the first page of messages in the account.
func ListMessages(ctx context.Context, client *Client, accountID string) ([]Message, error) {
	found, _, err := queryMessages(ctx, client, accountID, nil, mailbackend.Page{}, false)
	return found, err
}
