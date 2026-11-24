// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Impakta-io/inbai/internal/mailbackend"
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
// A cannotCalculateChanges method error is mailbackend.ErrChangesLost. Stalwart
// returns it when the change log since that state is gone, and also for an
// account that no longer exists.
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
		var problem *methodError
		if errors.As(err, &problem) && problem.Type == "cannotCalculateChanges" {
			return mailDelta{}, fmt.Errorf("%w: %w", mailbackend.ErrChangesLost, err)
		}
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
	created := parsed.Created
	if len(created) > 0 {
		created, err = filterInboxMessages(ctx, client, accountID, created)
		if err != nil {
			return mailDelta{}, err
		}
	}
	return mailDelta{
		State:     parsed.NewState,
		Created:   created,
		Destroyed: parsed.Destroyed,
		More:      parsed.HasMoreChanges,
	}, nil
}

func filterInboxMessages(ctx context.Context, client *Client, accountID string, ids []string) ([]string, error) {
	found, err := client.invokeMany(ctx, []jmapCall{
		{Name: "Mailbox/query", ID: "b", Args: map[string]any{
			"accountId": accountID,
			"filter":    map[string]any{"role": "inbox"},
		}},
		{Name: "Email/get", ID: "g", Args: map[string]any{
			"accountId":  accountID,
			"ids":        ids,
			"properties": []string{"mailboxIds"},
		}},
	})
	if err != nil {
		return nil, err
	}
	var boxes struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(found["b"], &boxes); err != nil {
		return nil, err
	}
	if len(boxes.IDs) == 0 {
		return nil, fmt.Errorf("account %s has no inbox", accountID)
	}
	inboxID := boxes.IDs[0]
	var got struct {
		List []struct {
			ID         string          `json:"id"`
			MailboxIDs map[string]bool `json:"mailboxIds"`
		} `json:"list"`
	}
	if err := json.Unmarshal(found["g"], &got); err != nil {
		return nil, err
	}
	inInbox := make(map[string]bool, len(got.List))
	for _, msg := range got.List {
		if msg.MailboxIDs[inboxID] {
			inInbox[msg.ID] = true
		}
	}
	out := make([]string, 0, len(inInbox))
	for _, id := range ids {
		if inInbox[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

type mailDelta struct {
	State     string
	Created   []string
	Destroyed []string
	More      bool
}
