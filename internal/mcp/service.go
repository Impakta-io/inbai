// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

// ServiceBox calls the service layer for one caller.
type ServiceBox struct {
	Service *service.Service
	Caller  service.Caller
	// Public, when set, replaces a service error with the error the tool
	// caller sees. A tool result shows the error text, and the text of an
	// unexpected backend error can name hosts and accounts. The HTTP
	// transport sets it so MCP hides what REST hides.
	Public func(error) error
}

func (s ServiceBox) public(err error) error {
	if err == nil || s.Public == nil {
		return err
	}
	return s.Public(err)
}

func (s ServiceBox) List(ctx context.Context, inbox, query string) ([]mailbackend.Message, error) {
	if query != "" {
		messages, _, err := s.Service.Search(ctx, s.Caller, inbox, query, mailbackend.Page{})
		return messages, s.public(err)
	}
	messages, _, err := s.Service.List(ctx, s.Caller, inbox, mailbackend.Page{})
	return messages, s.public(err)
}

func (s ServiceBox) Get(ctx context.Context, inbox, messageID string) (mailbackend.Message, error) {
	message, err := s.Service.Get(ctx, s.Caller, inbox, messageID)
	return message, s.public(err)
}

func (s ServiceBox) Send(ctx context.Context, inbox string, msg mailbackend.Outbound) error {
	msg.AccountID = inbox
	return s.public(s.Service.Deliver(ctx, s.Caller, msg, ""))
}

func (s ServiceBox) Reply(ctx context.Context, inbox, messageID string, msg mailbackend.Outbound) error {
	return s.public(s.Service.Reply(ctx, s.Caller, inbox, messageID, msg, ""))
}

func (s ServiceBox) Delete(ctx context.Context, inbox, messageID string) error {
	return s.public(s.Service.Delete(ctx, s.Caller, inbox, messageID))
}

// Wait reads the event feed. A nil after starts from the newest event when the call begins,
// so a change recorded while it waits is returned and older history is not.
func (s ServiceBox) Wait(ctx context.Context, inbox string, after *int64, timeout time.Duration) ([]mailbackend.Message, int64, error) {
	var from int64
	if after != nil {
		from = *after
	} else {
		head, err := s.Service.EventHead(ctx, s.Caller, inbox)
		if err != nil {
			return nil, 0, s.public(err)
		}
		from = head
	}
	records, err := s.Service.Events(ctx, s.Caller, inbox, from, timeout)
	if err != nil {
		return nil, 0, s.public(err)
	}
	out := make([]mailbackend.Message, len(records))
	for i, record := range records {
		out[i] = mailbackend.Message{ID: record.MessageID, Subject: record.Kind}
		from = record.ID
	}
	return out, from, nil
}
