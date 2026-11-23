// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"fmt"
)

// SearchMessages returns messages whose text matches query.
func SearchMessages(ctx context.Context, client *Client, accountID, query string) ([]Message, error) {
	return messages(ctx, client, accountID, map[string]any{"text": query}, false)
}

// GetMessage returns one message, including its plain-text body and threading headers.
func GetMessage(ctx context.Context, client *Client, accountID, messageID string) (Message, error) {
	if client == nil {
		return Message{}, fmt.Errorf("missing stalwart client")
	}
	if accountID == "" || messageID == "" {
		return Message{}, fmt.Errorf("message needs an account and an id")
	}
	raw, err := client.invoke(ctx, "g", "Email/get", map[string]any{
		"accountId":          accountID,
		"ids":                []string{messageID},
		"properties":         []string{"subject", "preview", "messageId", "inReplyTo", "references", "textBody", "bodyValues"},
		"fetchAllBodyValues": true,
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
func DeleteMessage(ctx context.Context, client *Client, accountID, messageID string) error {
	if client == nil {
		return fmt.Errorf("missing stalwart client")
	}
	if accountID == "" || messageID == "" {
		return fmt.Errorf("message needs an account and an id")
	}
	_, err := client.invoke(ctx, "e", "Email/set", map[string]any{
		"accountId": accountID,
		"destroy":   []string{messageID},
	})
	return err
}

func messages(ctx context.Context, client *Client, accountID string, filter map[string]any, full bool) ([]Message, error) {
	if client == nil {
		return nil, fmt.Errorf("missing stalwart client")
	}
	args := map[string]any{"accountId": accountID, "limit": 50}
	if filter != nil {
		args["filter"] = filter
	}
	raw, err := client.invoke(ctx, "q", "Email/query", args)
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
	props := []string{"subject", "preview"}
	if full {
		props = []string{"subject", "preview", "messageId", "inReplyTo", "references", "textBody", "bodyValues"}
	}
	raw, err = client.invoke(ctx, "g", "Email/get", map[string]any{
		"accountId":  accountID,
		"ids":        query.IDs,
		"properties": props,
	})
	if err != nil {
		return nil, err
	}
	return decodeMessages(raw, full)
}

func decodeMessages(raw json.RawMessage, full bool) ([]Message, error) {
	var got struct {
		List []struct {
			ID         string   `json:"id"`
			Subject    string   `json:"subject"`
			Preview    string   `json:"preview"`
			MessageID  []string `json:"messageId"`
			InReplyTo  []string `json:"inReplyTo"`
			References []string `json:"references"`
			TextBody   []struct {
				PartID string `json:"partId"`
			} `json:"textBody"`
			BodyValues map[string]struct {
				Value string `json:"value"`
			} `json:"bodyValues"`
		} `json:"list"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, err
	}
	out := make([]Message, len(got.List))
	for i, item := range got.List {
		out[i] = Message{ID: item.ID, Subject: item.Subject, Preview: item.Preview}
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
	}
	return out, nil
}
