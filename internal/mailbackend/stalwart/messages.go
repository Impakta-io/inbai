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

// SearchMessages returns messages whose text matches query.
func SearchMessages(ctx context.Context, client *Client, accountID, query string) ([]Message, error) {
	found, _, err := queryMessages(ctx, client, accountID, map[string]any{"text": query}, mailbackend.Page{}, false)
	return found, err
}

// maxBodyValueBytes caps the text body Stalwart returns for one message.
// A longer body arrives cut at this size, with isTruncated set on the part.
// Only the text part is read; HTML is never used, so it is not fetched.
const maxBodyValueBytes = 1 << 20

// GetMessage returns one message, including its plain-text body and threading headers.
func GetMessage(ctx context.Context, client *Client, accountID, messageID string) (Message, error) {
	if client == nil {
		return Message{}, fmt.Errorf("missing stalwart client")
	}
	if accountID == "" || messageID == "" {
		return Message{}, fmt.Errorf("message needs an account and an id")
	}
	raw, err := client.invoke(ctx, "g", "Email/get", map[string]any{
		"accountId":           accountID,
		"ids":                 []string{messageID},
		"properties":          []string{"subject", "preview", "receivedAt", "from", "messageId", "inReplyTo", "references", "textBody", "bodyValues", "attachments", "keywords", "header:Authentication-Results:asText"},
		"fetchTextBodyValues": true,
		"maxBodyValueBytes":   maxBodyValueBytes,
	})
	if err != nil {
		return Message{}, err
	}
	list, err := decodeMessages(raw, true)
	if err != nil {
		return Message{}, err
	}
	if len(list) == 0 {
		return Message{}, fmt.Errorf("account %s has no message %s", accountID, messageID)
	}
	return list[0], nil
}

// DeleteMessage removes one message from the account.
// A message that is already gone counts as deleted, as it does for the IMAP backend.
// Any other notDestroyed entry is a mailbackend.RefusedError.
func DeleteMessage(ctx context.Context, client *Client, accountID, messageID string) error {
	if client == nil {
		return fmt.Errorf("missing stalwart client")
	}
	if accountID == "" || messageID == "" {
		return fmt.Errorf("message needs an account and an id")
	}
	raw, err := client.invoke(ctx, "e", "Email/set", map[string]any{
		"accountId": accountID,
		"destroy":   []string{messageID},
	})
	if err != nil {
		return err
	}
	var body struct {
		NotDestroyed map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"notDestroyed"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	if missing, ok := body.NotDestroyed[messageID]; ok {
		if missing.Type == "notFound" {
			return nil
		}
		refused := &mailbackend.RefusedError{Type: missing.Type, Description: missing.Description}
		return fmt.Errorf("stalwart did not delete the message: stalwart %s: %w", messageID, refused)
	}
	if err := destroyedID(raw, messageID); err != nil {
		return fmt.Errorf("stalwart did not delete the message: %w", err)
	}
	return nil
}

func queryMessages(ctx context.Context, client *Client, accountID string, filter map[string]any, page mailbackend.Page, full bool) ([]Message, string, error) {
	if client == nil {
		return nil, "", fmt.Errorf("missing stalwart client")
	}
	limit, err := page.Bound()
	if err != nil {
		return nil, "", err
	}
	inboxID, err := inboxMailboxID(ctx, client, accountID)
	if err != nil {
		return nil, "", err
	}
	combined := map[string]any{"inMailbox": inboxID}
	for k, v := range filter {
		combined[k] = v
	}
	args := map[string]any{
		"accountId": accountID,
		"limit":     limit + 1,
		"filter":    combined,
	}
	if page.After != "" {
		args["anchor"] = page.After
		args["anchorOffset"] = 1
	}
	raw, err := client.invoke(ctx, "q", "Email/query", args)
	if err != nil {
		if strings.Contains(err.Error(), "anchorNotFound") {
			return nil, "", mailbackend.ErrPage
		}
		return nil, "", err
	}
	var query struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(raw, &query); err != nil {
		return nil, "", err
	}
	ids := query.IDs
	next := ""
	if len(ids) > limit {
		next = ids[limit-1]
		ids = ids[:limit]
	}
	if len(ids) == 0 {
		return nil, "", nil
	}
	props := []string{"subject", "preview", "receivedAt"}
	if full {
		props = []string{"subject", "preview", "receivedAt", "messageId", "inReplyTo", "references", "textBody", "bodyValues"}
	}
	raw, err = client.invoke(ctx, "g", "Email/get", map[string]any{
		"accountId":  accountID,
		"ids":        ids,
		"properties": props,
	})
	if err != nil {
		return nil, "", err
	}
	found, err := decodeMessages(raw, full)
	return found, next, err
}

func decodeMessages(raw json.RawMessage, full bool) ([]Message, error) {
	var got struct {
		List []struct {
			ID         string    `json:"id"`
			Subject    string    `json:"subject"`
			Preview    string    `json:"preview"`
			MessageID  []string  `json:"messageId"`
			InReplyTo  []string  `json:"inReplyTo"`
			References []string  `json:"references"`
			ReceivedAt time.Time `json:"receivedAt"`
			TextBody   []struct {
				PartID string `json:"partId"`
			} `json:"textBody"`
			BodyValues map[string]struct {
				Value string `json:"value"`
			} `json:"bodyValues"`
			Attachments []struct {
				Name   string `json:"name"`
				Type   string `json:"type"`
				Size   int    `json:"size"`
				BlobID string `json:"blobId"`
			} `json:"attachments"`
			Keywords json.RawMessage `json:"keywords"`
			Auth     json.RawMessage `json:"header:Authentication-Results:asText"`
			From     json.RawMessage `json:"from"`
		} `json:"list"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, err
	}
	out := make([]Message, len(got.List))
	for i, item := range got.List {
		out[i] = Message{ID: item.ID, Subject: item.Subject, Preview: item.Preview, ReceivedAt: item.ReceivedAt}
		if !full {
			continue
		}
		out[i].MessageID = item.MessageID
		out[i].InReplyTo = item.InReplyTo
		out[i].References = item.References
		part := "p"
		if len(item.TextBody) > 0 && item.TextBody[0].PartID != "" {
			part = item.TextBody[0].PartID
		}
		if body, ok := item.BodyValues[part]; ok {
			out[i].Text = body.Value
		}
		for _, file := range item.Attachments {
			out[i].Attachments = append(out[i].Attachments, Attachment{
				Name: file.Name, Type: file.Type, Size: file.Size, BlobID: file.BlobID,
			})
		}
		out[i].From = fromEmail(item.From)
		out[i].Authentication = mailbackend.AuthenticationOf(headerText(item.Auth), junkKeyword(item.Keywords))
	}
	return out, nil
}

func headerText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
		return many[0]
	}
	return ""
}

func fromEmail(raw json.RawMessage) string {
	var many []struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
		return many[0].Email
	}
	return ""
}

func junkKeyword(raw json.RawMessage) bool {
	var keywords map[string]bool
	if json.Unmarshal(raw, &keywords) != nil {
		return false
	}
	return keywords["$junk"]
}
