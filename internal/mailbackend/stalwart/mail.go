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
	return CreateInbox(ctx, m.Client, InboxInput{DomainID: spec.DomainID, RoleID: spec.RoleID, Local: spec.Local, QuotaBytes: spec.QuotaBytes})
}

func (m Mail) CreateInboxes(ctx context.Context, specs []mailbackend.InboxSpec) ([]string, error) {
	in := make([]InboxInput, len(specs))
	for i, spec := range specs {
		in[i] = InboxInput{DomainID: spec.DomainID, RoleID: spec.RoleID, Local: spec.Local, QuotaBytes: spec.QuotaBytes}
	}
	return CreateInboxes(ctx, m.Client, in)
}

func (m Mail) GetStorageQuota(ctx context.Context, accountID string) (mailbackend.StorageQuota, error) {
	return GetStorageQuota(ctx, m.Client, accountID)
}

func (m Mail) SetStorageQuota(ctx context.Context, accountID string, limitBytes int64) error {
	return SetStorageQuota(ctx, m.Client, accountID, limitBytes)
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

func (m Mail) List(ctx context.Context, accountID string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	raw, next, err := queryMessages(ctx, m.Client, accountID, nil, page, false)
	if err != nil {
		return nil, "", err
	}
	return toMessages(raw), next, nil
}

func (m Mail) Search(ctx context.Context, accountID, query string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	raw, next, err := queryMessages(ctx, m.Client, accountID, map[string]any{"text": query}, page, false)
	if err != nil {
		return nil, "", err
	}
	return toMessages(raw), next, nil
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

func (m Mail) ListInboxes(ctx context.Context, page mailbackend.Page) ([]mailbackend.Account, string, error) {
	raw, next, err := ListInboxes(ctx, m.Client, page)
	if err != nil {
		return nil, "", err
	}
	out := make([]mailbackend.Account, len(raw))
	for i, account := range raw {
		out[i] = mailbackend.Account{ID: account.ID, Local: account.Local}
	}
	return out, next, nil
}

func (m Mail) DeleteInbox(ctx context.Context, id string) error {
	return DeleteInbox(ctx, m.Client, id)
}

func (m Mail) Download(ctx context.Context, accountID, blobID string) ([]byte, error) {
	return m.Client.download(ctx, accountID, blobID)
}

func (m Mail) MailState(ctx context.Context, accountID string) (string, error) {
	return MailState(ctx, m.Client, accountID)
}

func (m Mail) MailChanges(ctx context.Context, accountID, since string) (mailbackend.Delta, error) {
	delta, err := MailChanges(ctx, m.Client, accountID, since)
	if err != nil {
		return mailbackend.Delta{}, err
	}
	return mailbackend.Delta{State: delta.State, Created: delta.Created, Destroyed: delta.Destroyed, More: delta.More}, nil
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
		files[i] = mailbackend.Attachment{Name: file.Name, Type: file.Type, Size: file.Size, BlobID: file.BlobID}
	}
	return mailbackend.Message{
		ID:             message.ID,
		Subject:        message.Subject,
		Preview:        message.Preview,
		Text:           message.Text,
		MessageID:      message.MessageID,
		InReplyTo:      message.InReplyTo,
		References:     message.References,
		ReceivedAt:     message.ReceivedAt,
		From:           message.From,
		Attachments:    files,
		Authentication: message.Authentication,
	}
}
