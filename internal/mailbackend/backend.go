// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import "context"

// InboxSpec is an address to create in a domain.
type InboxSpec struct {
	DomainID string
	RoleID   string
	Local    string
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
	ID          string       `json:"id"`
	Subject     string       `json:"subject"`
	Preview     string       `json:"preview"`
	Text        string       `json:"text,omitempty"`
	MessageID   []string     `json:"messageId,omitempty"`
	InReplyTo   []string     `json:"inReplyTo,omitempty"`
	References  []string     `json:"references,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Backend is how the service layer stores and sends mail.
type Backend interface {
	CreateInbox(context.Context, InboxSpec) (string, error)
	CreateInboxes(context.Context, []InboxSpec) ([]string, error)
	Deliver(context.Context, Outbound) error
	List(context.Context, string) ([]Message, error)
	Search(context.Context, string, string) ([]Message, error)
	Get(context.Context, string, string) (Message, error)
	Delete(context.Context, string, string) error
	ListInboxes(context.Context) ([]Account, error)
	DeleteInbox(context.Context, string) error
	Download(context.Context, string, string) ([]byte, error)
}
