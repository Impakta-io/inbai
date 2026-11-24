// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

func TestSendIsOnlyAsTheInbox(t *testing.T) {
	ctx := context.Background()
	mail := &fakeMail{senders: map[string][]string{"inbox-a": {"ada@example.com", "Sales@Example.com"}}}
	svc := New(mail)
	svc.Allow("ada", "inbox-a", PermSend)
	for _, caller := range []Caller{{Agent: "ada"}, {Operator: true}} {
		msg := mailbackend.Outbound{AccountID: "inbox-a", From: "ceo@victim.test", To: "bob@example.com", Subject: "hi"}
		err := svc.Deliver(ctx, caller, msg, "")
		if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "not an address this inbox may send as") {
			t.Fatalf("%+v foreign from err %v", caller, err)
		}
		msg.From = "bea@example.com"
		if err := svc.Deliver(ctx, caller, msg, ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%+v other inbox address err %v", caller, err)
		}
	}
	if mail.sent != 0 {
		t.Fatalf("a refused sender reached the backend %d times", mail.sent)
	}
	if err := svc.Deliver(ctx, Caller{Agent: "ada"}, mailbackend.Outbound{AccountID: "inbox-a", To: "bob@example.com", Subject: "hi"}, ""); err != nil {
		t.Fatal(err)
	}
	if mail.last.From != "ada@example.com" {
		t.Fatalf("empty from sent as %q, want the inbox address", mail.last.From)
	}
	if err := svc.Deliver(ctx, Caller{Agent: "ada"}, mailbackend.Outbound{AccountID: "inbox-a", From: "Sales <sales@example.com>", To: "bob@example.com", Subject: "hi"}, ""); err != nil {
		t.Fatal(err)
	}
	if mail.last.From != "Sales@Example.com" {
		t.Fatalf("alias sent as %q, want the identity address", mail.last.From)
	}
}

func TestDomainQuotaIsKeyedOnTheInboxDomain(t *testing.T) {
	ctx := context.Background()
	mail := &fakeMail{senders: map[string][]string{"inbox-a": {"ada@one.test"}, "inbox-b": {"bea@one.test"}}}
	svc := New(mail)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	svc.SetSendLimits(10, 1, 10, 10)
	if err := svc.Deliver(ctx, Caller{Operator: true}, mailbackend.Outbound{AccountID: "inbox-a", To: "x@far.test"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := svc.sent["domain\none.test\n2026-11-24"]; got != 1 {
		t.Fatalf("one.test bucket %d, sent %v", got, svc.sent)
	}
	if err := svc.Deliver(ctx, Caller{Operator: true}, mailbackend.Outbound{AccountID: "inbox-b", From: "b@fresh.test", To: "x@far.test"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("fresh domain err %v", err)
	}
	if err := svc.Deliver(ctx, Caller{Operator: true}, mailbackend.Outbound{AccountID: "inbox-b", To: "x@far.test"}, ""); !errors.Is(err, ErrQuota) {
		t.Fatalf("same domain err %v", err)
	}
	if mail.sent != 1 {
		t.Fatalf("backend sends %d, want 1", mail.sent)
	}
}

func TestReplyGoesToTheOriginalSender(t *testing.T) {
	ctx := context.Background()
	original := mailbackend.Message{ID: "m", Subject: "hello", From: "Ada <ada@sender.test>", MessageID: []string{"<m@sender.test>"}}
	mail := &fakeMail{
		messages: []mailbackend.Message{original},
		senders:  map[string][]string{"inbox-a": {"me@example.com"}},
	}
	svc := New(mail)
	svc.Allow("rex", "inbox-a", PermReply)
	rex := Caller{Agent: "rex"}

	if err := svc.Reply(ctx, rex, "inbox-a", "m", mailbackend.Outbound{Body: "thanks"}, ""); err != nil {
		t.Fatal(err)
	}
	if mail.last.To != "ada@sender.test" || mail.last.From != "me@example.com" || mail.last.Subject != "Re: hello" {
		t.Fatalf("reply %+v", mail.last)
	}
	if err := svc.Reply(ctx, rex, "inbox-a", "m", mailbackend.Outbound{To: "ADA@sender.test"}, ""); err != nil || mail.last.To != "ada@sender.test" {
		t.Fatalf("same sender to %q err %v", mail.last.To, err)
	}
	sent := mail.sent
	err := svc.Reply(ctx, rex, "inbox-a", "m", mailbackend.Outbound{To: "eve@elsewhere.test"}, "")
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "needs send") {
		t.Fatalf("reply-only to another address err %v", err)
	}
	if err := svc.Reply(ctx, rex, "inbox-a", "m", mailbackend.Outbound{From: "ceo@victim.test"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reply as a foreign sender err %v", err)
	}
	if mail.sent != sent {
		t.Fatalf("a refused reply reached the backend")
	}

	svc.Allow("rex", "inbox-a", PermSend)
	if err := svc.Reply(ctx, rex, "inbox-a", "m", mailbackend.Outbound{To: "eve@elsewhere.test"}, ""); err != nil || mail.last.To != "eve@elsewhere.test" {
		t.Fatalf("reply with send to %q err %v", mail.last.To, err)
	}
	if err := svc.Reply(ctx, Caller{Operator: true}, "inbox-a", "m", mailbackend.Outbound{To: "eve@elsewhere.test"}, ""); err != nil {
		t.Fatalf("operator reply err %v", err)
	}
}

func TestReplyPrefersReplyTo(t *testing.T) {
	ctx := context.Background()
	mail := &fakeMail{messages: []mailbackend.Message{{
		ID: "m", Subject: "hello", From: "ada@sender.test", ReplyTo: "Help Desk <help@sender.test>, other@sender.test",
	}}}
	svc := New(mail)
	svc.Allow("rex", "inbox-a", PermReply)
	if err := svc.Reply(ctx, Caller{Agent: "rex"}, "inbox-a", "m", mailbackend.Outbound{}, ""); err != nil || mail.last.To != "help@sender.test" {
		t.Fatalf("reply to %q err %v", mail.last.To, err)
	}
	if err := svc.Reply(ctx, Caller{Agent: "rex"}, "inbox-a", "m", mailbackend.Outbound{To: "ada@sender.test"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reply-only to From instead of Reply-To err %v", err)
	}
}

func TestSendFailsClosedWhenTheSenderLookupFails(t *testing.T) {
	ctx := context.Background()
	down := errors.New("identity lookup down")
	mail := &fakeMail{
		messages: []mailbackend.Message{{ID: "m", Subject: "hello", From: "ada@sender.test"}},
		senders:  map[string][]string{"inbox-a": {}},
		lookErr:  down,
	}
	svc := New(mail)
	for _, from := range []string{"", "inbox-a@example.com"} {
		msg := mailbackend.Outbound{AccountID: "inbox-a", From: from, To: "bob@example.com", Subject: "hi"}
		if err := svc.Deliver(ctx, Caller{Operator: true}, msg, ""); !errors.Is(err, down) {
			t.Fatalf("send from %q with a failed lookup err %v", from, err)
		}
		if err := svc.Reply(ctx, Caller{Operator: true}, "inbox-a", "m", mailbackend.Outbound{From: from}, ""); !errors.Is(err, down) {
			t.Fatalf("reply from %q with a failed lookup err %v", from, err)
		}
	}
	mail.lookErr = nil
	err := svc.Deliver(ctx, Caller{Operator: true}, mailbackend.Outbound{AccountID: "inbox-a", To: "bob@example.com", Subject: "hi"}, "")
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "has no address it may send as") {
		t.Fatalf("send with no sender address err %v", err)
	}
	if mail.sent != 0 {
		t.Fatalf("an unchecked sender reached the backend %d times", mail.sent)
	}
}

func TestReplyNeedsAnAddressToReplyTo(t *testing.T) {
	ctx := context.Background()
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "hello", From: "undisclosed"}}}
	svc := New(mail)
	svc.Allow("rex", "inbox-a", PermReply)
	for _, caller := range []Caller{{Agent: "rex"}, {Operator: true}} {
		err := svc.Reply(ctx, caller, "inbox-a", "m", mailbackend.Outbound{To: "bob@example.com"}, "")
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "no From or Reply-To") {
			t.Fatalf("%+v err %v", caller, err)
		}
	}
	if mail.sent != 0 {
		t.Fatalf("backend sends %d", mail.sent)
	}
}

func TestFirstAddress(t *testing.T) {
	for in, want := range map[string]string{
		"ada@example.com":                       "ada@example.com",
		"Ada <Ada@Example.com>":                 "Ada@Example.com",
		`"Doe, Jane" <jane@example.com>, bob`:   "jane@example.com",
		"a@example.com, b@example.com":          "a@example.com",
		"=?x-unknown?q?Ada?= <ada@example.com>": "ada@example.com",
		"":                                      "",
		"undisclosed-recipients:;":              "",
		"not an address":                        "",
	} {
		if got := firstAddress(in); got != want {
			t.Errorf("firstAddress(%q) = %q, want %q", in, got, want)
		}
	}
}
