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

func (m Mail) CreateInboxes(ctx context.Context, specs []mailbackend.InboxSpec) ([]string, error) {
	in := make([]InboxInput, len(specs))
	for i, spec := range specs {
		in[i] = InboxInput{DomainID: spec.DomainID, RoleID: spec.RoleID, Local: spec.Local}
	}
	return CreateInboxes(ctx, m.Client, in)
}

func (m Mail) Deliver(ctx context.Context, msg mailbackend.Outbound) error {
	return Deliver(ctx, m.Client, Delivery{
		AccountID:   msg.AccountID,
		From:        msg.From,
		To:          msg.To,
		Subject:     msg.Subject,
		Body:        msg.Body,
		InReplyTo:   msg.InReplyTo,
		References:  msg.References,
		Attachments: msg.Attachments,
	})
}

func (m Mail) List(ctx context.Context, accountID string) ([]mailbackend.Message, error) {
	raw, err := ListMessages(ctx, m.Client, accountID)
	if err != nil {
		return nil, err
	}
	return toMessages(raw), nil
}

func (m Mail) Search(ctx context.Context, accountID, query string) ([]mailbackend.Message, error) {
	raw, err := SearchMessages(ctx, m.Client, accountID, query)
	if err != nil {
		return nil, err
	}
	return toMessages(raw), nil
}

func (m Mail) Get(ctx context.Context, accountID, messageID string) (mailbackend.Message, error) {
	raw, err := GetMessage(ctx, m.Client, accountID, messageID)
	if err != nil {
		return mailbackend.Message{}, err
	}
	return toMessage(raw), nil
}

func (m Mail) Delete(ctx context.Context, accountID, messageID string) error {
	return DeleteMessage(ctx, m.Client, accountID, messageID)
}

func toMessages(raw []Message) []mailbackend.Message {
	out := make([]mailbackend.Message, len(raw))
	for i, message := range raw {
		out[i] = toMessage(message)
	}
	return out
}

func toMessage(message Message) mailbackend.Message {
	files := make([]mailbackend.Attachment, len(message.Attachments))
	for i, file := range message.Attachments {
		files[i] = mailbackend.Attachment{Name: file.Name, Type: file.Type, Size: file.Size}
	}
	return mailbackend.Message{
		ID:          message.ID,
		Subject:     message.Subject,
		Preview:     message.Preview,
		Text:        message.Text,
		MessageID:   message.MessageID,
		InReplyTo:   message.InReplyTo,
		References:  message.References,
		Attachments: files,
	}
}
