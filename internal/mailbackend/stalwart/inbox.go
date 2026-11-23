// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

// InboxInput names the account to create inside an existing domain.
type InboxInput struct {
	DomainID string
	RoleID   string
	Local    string
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
	ID          string       `json:"id"`
	Subject     string       `json:"subject"`
	Preview     string       `json:"preview"`
	Text        string       `json:"text,omitempty"`
	MessageID   []string     `json:"messageId,omitempty"`
	InReplyTo   []string     `json:"inReplyTo,omitempty"`
	References  []string     `json:"references,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is one file on a stored message.
type Attachment struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size int    `json:"size"`
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
			"a": account(local, in.DomainID, in.RoleID, secret),
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
	identity, err := client.invoke(ctx, "i", "Identity/get", map[string]any{
		"accountId": msg.AccountID,
		"ids":       nil,
	})
	if err != nil {
		return err
	}
	var identities struct {
		List []struct {
			ID string `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(identity, &identities); err != nil {
		return err
	}
	if len(identities.List) == 0 || identities.List[0].ID == "" {
		return fmt.Errorf("account %s has no identity", msg.AccountID)
	}
	boxes, err := client.invoke(ctx, "q", "Mailbox/query", map[string]any{
		"accountId": msg.AccountID,
		"filter":    map[string]any{"role": "inbox"},
	})
	if err != nil {
		return err
	}
	var query struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(boxes, &query); err != nil {
		return err
	}
	if len(query.IDs) == 0 {
		return fmt.Errorf("account %s has no inbox", msg.AccountID)
	}
	draft := map[string]any{
		"mailboxIds": map[string]bool{query.IDs[0]: true},
		"from":       []any{map[string]any{"email": msg.From}},
		"to":         []any{map[string]any{"email": msg.To}},
		"subject":    msg.Subject,
		"keywords":   map[string]bool{"$draft": true},
		"textBody":   []any{map[string]any{"partId": "p", "type": "text/plain"}},
		"bodyValues": map[string]any{"p": map[string]any{"value": msg.Body}},
	}
	if len(msg.InReplyTo) > 0 {
		draft["inReplyTo"] = msg.InReplyTo
	}
	if len(msg.References) > 0 {
		draft["references"] = msg.References
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
		draft["attachments"] = files
	}
	_, err = client.invokeMany(ctx, []jmapCall{
		{Name: "Email/set", ID: "e", Args: map[string]any{
			"accountId": msg.AccountID,
			"create":    map[string]any{"m": draft},
		}},
		{Name: "EmailSubmission/set", ID: "s", Args: map[string]any{
			"accountId": msg.AccountID,
			"create": map[string]any{
				"s": map[string]any{
					"emailId":    "#m",
					"identityId": identities.List[0].ID,
					"envelope": map[string]any{
						"mailFrom": map[string]any{"email": msg.From},
						"rcptTo":   []any{map[string]any{"email": msg.To}},
					},
				},
			},
		}},
	})
	return err
}

// ListMessages returns the messages currently in the account.
func ListMessages(ctx context.Context, client *Client, accountID string) ([]Message, error) {
	if client == nil {
		return nil, fmt.Errorf("missing stalwart client")
	}
	raw, err := client.invoke(ctx, "q", "Email/query", map[string]any{
		"accountId": accountID,
		"limit":     50,
	})
	if err != nil {
		return nil, err
	}
	var query struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(raw, &query); err != nil {
		return nil, err
	}
	if len(query.IDs) == 0 {
		return nil, nil
	}
	raw, err = client.invoke(ctx, "g", "Email/get", map[string]any{
		"accountId":  accountID,
		"ids":        query.IDs,
		"properties": []string{"subject", "preview"},
	})
	if err != nil {
		return nil, err
	}
	var got struct {
		List []Message `json:"list"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, err
	}
	return got.List, nil
}
