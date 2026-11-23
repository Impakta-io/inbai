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
	if err := svc.Deliver(context.Background(), Caller{Agent: "ada"}, mailbackend.Outbound{AccountID: "inbox-a"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("send without send grant err = %v", err)
	}
}

func TestOperatorCanCreateAnInbox(t *testing.T) {
	mail := &fakeMail{created: "id-1"}
	svc := New(mail)
	id, err := svc.CreateInbox(context.Background(), Caller{Operator: true}, mailbackend.InboxSpec{Local: "alice"}, "")
	if err != nil || id != "id-1" {
		t.Fatalf("create %s %v", id, err)
	}
	if _, err := svc.CreateInbox(context.Background(), Caller{Agent: "ada"}, mailbackend.InboxSpec{Local: "bob"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent create err = %v", err)
	}
}

func TestSendQuotaAndIdempotency(t *testing.T) {
	mail := &fakeMail{}
	svc := New(mail)
	svc.inboxDaily = 1
	svc.domainDaily = 2
	msg := mailbackend.Outbound{AccountID: "inbox-a", From: "a@example.com", To: "b@example.com", Subject: "hi"}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, msg, "key-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, msg, "key-1"); err != nil {
		t.Fatal(err)
	}
	if mail.sent != 1 {
		t.Fatalf("backend sends = %d", mail.sent)
	}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, msg, "key-2"); !errors.Is(err, ErrQuota) {
		t.Fatalf("quota err = %v", err)
	}
	if mail.sent != 1 {
		t.Fatalf("quota still called the backend, sends = %d", mail.sent)
	}
}

func TestFailedSendDoesNotConsumeQuota(t *testing.T) {
	mail := &fakeMail{sendErr: errors.New("down")}
	svc := New(mail)
	svc.inboxDaily = 1
	msg := mailbackend.Outbound{AccountID: "inbox-a", From: "a@example.com", To: "b@example.com"}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, msg, ""); err == nil {
		t.Fatal("expected send error")
	}
	mail.sendErr = nil
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, msg, ""); err != nil {
		t.Fatal(err)
	}
}

type fakeMail struct {
	created  string
	messages []mailbackend.Message
	listed   bool
	sent     int
	sendErr  error
}

func (f *fakeMail) CreateInbox(context.Context, mailbackend.InboxSpec) (string, error) {
	return f.created, nil
}

func (f *fakeMail) Deliver(context.Context, mailbackend.Outbound) error {
	f.sent++
	return f.sendErr
}

func (f *fakeMail) List(context.Context, string) ([]mailbackend.Message, error) {
	f.listed = true
	return f.messages, nil
}
