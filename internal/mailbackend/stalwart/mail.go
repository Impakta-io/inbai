// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

// Mail is the Stalwart implementation of mailbackend.Backend.
type Mail struct {
	Client *Client
}

func (m Mail) CreateInbox(ctx context.Context, spec mailbackend.InboxSpec) (string, error) {
	return CreateInbox(ctx, m.Client, InboxInput{DomainID: spec.DomainID, RoleID: spec.RoleID, Local: spec.Local})
}

func (m Mail) Deliver(ctx context.Context, msg mailbackend.Outbound) error {
	return Deliver(ctx, m.Client, Delivery{
		AccountID: msg.AccountID,
		From:      msg.From,
		To:        msg.To,
		Subject:   msg.Subject,
		Body:      msg.Body,
	})
}

func (m Mail) List(ctx context.Context, accountID string) ([]mailbackend.Message, error) {
	raw, err := ListMessages(ctx, m.Client, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]mailbackend.Message, len(raw))
	for i, message := range raw {
		out[i] = mailbackend.Message{ID: message.ID, Subject: message.Subject, Preview: message.Preview}
	}
	return out, nil
}
