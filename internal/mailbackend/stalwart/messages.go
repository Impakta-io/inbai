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
// authservID is the authserv-id our Stalwart writes in Authentication-Results,
// which is its hostname. See mailbackend.AuthenticationOf.
func GetMessage(ctx context.Context, client *Client, accountID, messageID, authservID string) (Message, error) {
	if client == nil {
		return Message{}, fmt.Errorf("missing stalwart client")
	}
	if accountID == "" || messageID == "" {
		return Message{}, fmt.Errorf("message needs an account and an id")
	}
	// :all returns every Authentication-Results field in header order, top
	// first (RFC 8621 section 4.1.3). Without it, JMAP returns only the last
	// one, the one nearest the body, which the sender can write.
	raw, err := client.invoke(ctx, "g", "Email/get", map[string]any{
		"accountId":           accountID,
		"ids":                 []string{messageID},
		"properties":          []string{"subject", "preview", "receivedAt", "from", "replyTo", "messageId", "inReplyTo", "references", "textBody", "bodyValues", "attachments", "keywords", "header:Authentication-Results:asText:all"},
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
	list[0].Authentication = authenticationOf(raw, authservID)
	return list[0], nil
}

// authenticationOf reads the Authentication-Results fields and the junk
// keyword of the one message in an Email/get response.
func authenticationOf(raw json.RawMessage, authservID string) mailbackend.Authentication {
	var got struct {
		List []struct {
			Keywords json.RawMessage `json:"keywords"`
			Auth     json.RawMessage `json:"header:Authentication-Results:asText:all"`
		} `json:"list"`
	}
	if json.Unmarshal(raw, &got) != nil || len(got.List) == 0 {
		return mailbackend.Authentication{}
	}
	item := got.List[0]
	return mailbackend.AuthenticationOf(headerTexts(item.Auth), authservID, junkKeyword(item.Keywords))
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
		// Newest first. The anchor is an id looked up in this sorted result, so a
		// message that arrives between pages does not move the next page.
		"sort": []map[string]any{{"property": "receivedAt", "isAscending": false}},
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
			From    json.RawMessage `json:"from"`
			ReplyTo json.RawMessage `json:"replyTo"`
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
		out[i].ReplyTo = fromEmail(item.ReplyTo)
	}
	return out, nil
}

// headerTexts reads a header:...:asText:all value, an array of strings.
// Anything else reads as no headers.
func headerTexts(raw json.RawMessage) []string {
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return nil
	}
	return many
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
