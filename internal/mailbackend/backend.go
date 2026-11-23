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
	AccountID string
	From      string
	To        string
	Subject   string
	Body      string
}

// Message is one message stored for an inbox.
type Message struct {
	ID      string
	Subject string
	Preview string
}

// Backend is how the service layer stores and sends mail.
type Backend interface {
	CreateInbox(context.Context, InboxSpec) (string, error)
	Deliver(context.Context, Outbound) error
	List(context.Context, string) ([]Message, error)
}
