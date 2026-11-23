// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

func TestAgentWithoutAGrantCannotRead(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "secret"}}}
	svc := New(mail)
	_, err := svc.List(context.Background(), Caller{Agent: "eve"}, "inbox-a")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
	if mail.listed {
		t.Fatal("the mail backend was called without a grant")
	}
}

func TestReadGrantAllowsOnlyThatInbox(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "hello"}}}
	svc := New(mail)
	svc.Allow("ada", "inbox-a", PermRead)
	got, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a")
	if err != nil || len(got) != 1 {
		t.Fatalf("list %+v %v", got, err)
	}
	if _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-b"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other inbox err = %v", err)
	}
	if err := svc.Deliver(context.Background(), Caller{Agent: "ada"}, mailbackend.Outbound{AccountID: "inbox-a"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("send without send grant err = %v", err)
	}
}

func TestOperatorCanCreateAnInbox(t *testing.T) {
	mail := &fakeMail{created: "id-1"}
	svc := New(mail)
	id, err := svc.CreateInbox(context.Background(), Caller{Operator: true}, mailbackend.InboxSpec{Local: "alice"})
	if err != nil || id != "id-1" {
		t.Fatalf("create %s %v", id, err)
	}
	if _, err := svc.CreateInbox(context.Background(), Caller{Agent: "ada"}, mailbackend.InboxSpec{Local: "bob"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent create err = %v", err)
	}
}

type fakeMail struct {
	created  string
	messages []mailbackend.Message
	listed   bool
}

func (f *fakeMail) CreateInbox(context.Context, mailbackend.InboxSpec) (string, error) {
	return f.created, nil
}

func (f *fakeMail) Deliver(context.Context, mailbackend.Outbound) error { return nil }

func (f *fakeMail) List(context.Context, string) ([]mailbackend.Message, error) {
	f.listed = true
	return f.messages, nil
}
