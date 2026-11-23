// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"fmt"
)

// MailState returns the account's current Email state without listing messages.
func MailState(ctx context.Context, client *Client, accountID string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("missing stalwart client")
	}
	if accountID == "" {
		return "", fmt.Errorf("missing account")
	}
	raw, err := client.invoke(ctx, "t", "Email/query", map[string]any{
		"accountId": accountID,
		"limit":     0,
	})
	if err != nil {
		return "", err
	}
	var query struct {
		State      string `json:"state"`
		QueryState string `json:"queryState"`
	}
	if err := json.Unmarshal(raw, &query); err != nil {
		return "", err
	}
	if query.State == "" {
		query.State = query.QueryState
	}
	if query.State == "" {
		snippet := string(raw)
		if len(snippet) > 180 {
			snippet = snippet[:180]
		}
		return "", fmt.Errorf("stalwart returned no email state: %s", snippet)
	}
	return query.State, nil
}

// MailChanges returns messages created or destroyed since a JMAP state.
func MailChanges(ctx context.Context, client *Client, accountID, since string) (mailDelta, error) {
	if client == nil {
		return mailDelta{}, fmt.Errorf("missing stalwart client")
	}
	if accountID == "" || since == "" {
		return mailDelta{}, fmt.Errorf("changes need an account and a state")
	}
	raw, err := client.invoke(ctx, "c", "Email/changes", map[string]any{
		"accountId":  accountID,
		"sinceState": since,
		"maxChanges": 128,
	})
	if err != nil {
		return mailDelta{}, err
	}
	var parsed struct {
		NewState       string   `json:"newState"`
		HasMoreChanges bool     `json:"hasMoreChanges"`
		Created        []string `json:"created"`
		Destroyed      []string `json:"destroyed"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return mailDelta{}, err
	}
	if parsed.NewState == "" {
		return mailDelta{}, fmt.Errorf("stalwart returned no new email state")
	}
	return mailDelta{
		State:     parsed.NewState,
		Created:   parsed.Created,
		Destroyed: parsed.Destroyed,
		More:      parsed.HasMoreChanges,
	}, nil
}

type mailDelta struct {
	State     string
	Created   []string
	Destroyed []string
	More      bool
}
