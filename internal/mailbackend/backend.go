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

// Outbound is a message submitted as an existing account.
type Outbound struct {
	AccountID  string
	From       string
	To         string
	Subject    string
	Body       string
	InReplyTo  []string
	References []string
}

// Message is one message stored for an inbox.
type Message struct {
	ID         string   `json:"id"`
	Subject    string   `json:"subject"`
	Preview    string   `json:"preview"`
	Text       string   `json:"text,omitempty"`
	MessageID  []string `json:"messageId,omitempty"`
	InReplyTo  []string `json:"inReplyTo,omitempty"`
	References []string `json:"references,omitempty"`
}

// Backend is how the service layer stores and sends mail.
type Backend interface {
	CreateInbox(context.Context, InboxSpec) (string, error)
	Deliver(context.Context, Outbound) error
	List(context.Context, string) ([]Message, error)
	Search(context.Context, string, string) ([]Message, error)
	Get(context.Context, string, string) (Message, error)
	Delete(context.Context, string, string) error
}
