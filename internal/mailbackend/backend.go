// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import (
	"context"
	"errors"
	"time"
)

// ErrInvalid means the input holds bytes a backend must not write to its mail
// server, such as a line break in a search query.
var ErrInvalid = errors.New("invalid mail backend input")

// ErrExists means the mail server already has an account with the address a
// create asked for. The backend did not create or return that account.
var ErrExists = errors.New("mail account already exists")

// RefusedError means the mail server processed the request and refused one
// operation in it, such as a send over quota or from a sender it does not
// allow. Type and Description are the server's own reason, for example a JMAP
// SetError. A transport may show them to the caller. It does not show the text
// of other backend errors, which can name hosts and accounts.
type RefusedError struct {
	Type        string
	Description string
}

func (e *RefusedError) Error() string {
	if e.Description == "" {
		return e.Type
	}
	return e.Type + ": " + e.Description
}

// InboxSpec is an address to create in the backend's domain. Local is the
// part before the @. Where the account goes on the mail server, such as a
// domain or role record, is the backend's own configuration.
// QuotaBytes sets the account disk storage quota in bytes when greater than zero.
type InboxSpec struct {
	Local      string
	QuotaBytes int64
}

// StorageQuota is the disk storage cap and usage for one inbox, in bytes.
// LimitBytes zero means no storage cap is set on the account.
type StorageQuota struct {
	LimitBytes int64 `json:"limitBytes"`
	UsedBytes  int64 `json:"usedBytes"`
}

// QuotaBackend is implemented by mail backends that read and set per-inbox storage quotas.
type QuotaBackend interface {
	GetStorageQuota(ctx context.Context, accountID string) (StorageQuota, error)
	SetStorageQuota(ctx context.Context, accountID string, limitBytes int64) error
}

// File is one attachment submitted with a message.
type File struct {
	Name string
	Type string
	Data []byte
}

// Attachment is one file stored on a message.
type Attachment struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int    `json:"size"`
	BlobID string `json:"blobId,omitempty"`
}

// Account is one mailbox the backend already has.
type Account struct {
	ID    string
	Local string
}

// Outbound is a message submitted as an existing account.
type Outbound struct {
	AccountID   string
	From        string
	To          string
	Subject     string
	Body        string
	InReplyTo   []string
	References  []string
	Attachments []File
}

// Message is one message stored for an inbox.
type Message struct {
	ID             string         `json:"id"`
	Subject        string         `json:"subject"`
	Preview        string         `json:"preview"`
	Text           string         `json:"text,omitempty"`
	TextTruncated  bool           `json:"textTruncated,omitempty"`
	MessageID      []string       `json:"messageId,omitempty"`
	InReplyTo      []string       `json:"inReplyTo,omitempty"`
	References     []string       `json:"references,omitempty"`
	ReceivedAt     time.Time      `json:"receivedAt,omitempty"`
	From           string         `json:"from,omitempty"`
	ReplyTo        string         `json:"replyTo,omitempty"`
	Attachments    []Attachment   `json:"attachments,omitempty"`
	Authentication Authentication `json:"authentication,omitempty"`
	SenderAllowed  *bool          `json:"senderAllowed,omitempty"`
}

// Backend is how the service layer stores and sends mail.
type Backend interface {
	// CreateInbox returns the new account's id, or ErrExists when the address is taken.
	CreateInbox(context.Context, InboxSpec) (string, error)
	// CreateInboxes returns one id per spec, in order. An empty id means the
	// address was already taken and the backend did not create that account.
	CreateInboxes(context.Context, []InboxSpec) ([]string, error)
	Deliver(context.Context, Outbound) error
	// SenderAddresses returns the addresses the account may send as.
	// The first is the account's own address. The service refuses any other sender.
	SenderAddresses(ctx context.Context, accountID string) ([]string, error)
	List(context.Context, string, Page) ([]Message, string, error)
	Search(context.Context, string, string, Page) ([]Message, string, error)
	Get(context.Context, string, string) (Message, error)
	Delete(context.Context, string, string) error
	ListInboxes(context.Context, Page) ([]Account, string, error)
	DeleteInbox(context.Context, string) error
	Download(context.Context, string, string) ([]byte, error)
}
