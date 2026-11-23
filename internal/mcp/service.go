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
}

func (s ServiceBox) List(ctx context.Context, inbox, query string) ([]mailbackend.Message, error) {
	if query != "" {
		messages, _, err := s.Service.Search(ctx, s.Caller, inbox, query, mailbackend.Page{})
		return messages, err
	}
	messages, _, err := s.Service.List(ctx, s.Caller, inbox, mailbackend.Page{})
	return messages, err
}

func (s ServiceBox) Get(ctx context.Context, inbox, messageID string) (mailbackend.Message, error) {
	return s.Service.Get(ctx, s.Caller, inbox, messageID)
}

func (s ServiceBox) Send(ctx context.Context, inbox string, msg mailbackend.Outbound) error {
	msg.AccountID = inbox
	return s.Service.Deliver(ctx, s.Caller, msg, "")
}

func (s ServiceBox) Reply(ctx context.Context, inbox, messageID string, msg mailbackend.Outbound) error {
	return s.Service.Reply(ctx, s.Caller, inbox, messageID, msg, "")
}

func (s ServiceBox) Delete(ctx context.Context, inbox, messageID string) error {
	return s.Service.Delete(ctx, s.Caller, inbox, messageID)
}

func (s ServiceBox) Wait(ctx context.Context, inbox string, timeout time.Duration) ([]mailbackend.Message, error) {
	if s.Service.HasFeed() {
		records, err := s.Service.Events(ctx, s.Caller, inbox, 0, timeout)
		if err != nil {
			return nil, err
		}
		out := make([]mailbackend.Message, len(records))
		for i, record := range records {
			out[i] = mailbackend.Message{ID: record.MessageID, Subject: record.Kind}
		}
		return out, nil
	}
	deadline := time.Now().Add(timeout)
	for {
		messages, err := s.List(ctx, inbox, "")
		if err != nil || len(messages) > 0 || !time.Now().Before(deadline) {
			return messages, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
