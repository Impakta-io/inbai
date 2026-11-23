// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

func TestAgentCannotGrantItself(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "secret"}}}
	svc := New(mail)
	if err := svc.Grant(Caller{Agent: "ada"}, "ada", "inbox-a", PermRead); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a", mailbackend.Page{}); !errors.Is(err, ErrForbidden) || mail.listed {
		t.Fatalf("list err %v listed %v", err, mail.listed)
	}
	if err := svc.Grant(Caller{Operator: true}, "ada", "inbox-a", PermRead); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a", mailbackend.Page{}); err != nil || !mail.listed {
		t.Fatalf("granted list err %v listed %v", err, mail.listed)
	}
}

func TestAgentWithoutAGrantCannotRead(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "secret"}}}
	svc := New(mail)
	_, _, err := svc.List(context.Background(), Caller{Agent: "eve"}, "inbox-a", mailbackend.Page{})
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
	got, _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a", mailbackend.Page{})
	if err != nil || len(got) != 1 {
		t.Fatalf("list %+v %v", got, err)
	}
	if _, _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-b", mailbackend.Page{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other inbox err = %v", err)
	}
	if err := svc.Deliver(context.Background(), Caller{Agent: "ada"}, mailbackend.Outbound{AccountID: "inbox-a"}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("send without send grant err = %v", err)
	}
}

func TestAllowlistIsASignalOnTheMessage(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", From: "Ada <ada@example.com>", Text: "hi"}}}
	svc := New(mail)
	svc.Allow("ada", "inbox-a", PermRead)
	if err := svc.SetAllowlist(context.Background(), Caller{Agent: "ada"}, "inbox-a", []string{"ada@example.com"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read grant set err %v", err)
	}
	svc.Allow("ada", "inbox-b", PermManage)
	if err := svc.SetAllowlist(context.Background(), Caller{Agent: "ada"}, "inbox-a", []string{"ada@example.com"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other inbox set err %v", err)
	}
	if err := svc.SetAllowlist(context.Background(), Caller{Operator: true}, "inbox-a", []string{"not an address"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad sender err %v", err)
	}
	if err := svc.SetAllowlist(context.Background(), Caller{Operator: true}, "inbox-a", []string{"Example.com"}); err != nil {
		t.Fatal(err)
	}
	msg, err := svc.Get(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m")
	if err != nil || msg.SenderAllowed == nil || !*msg.SenderAllowed {
		t.Fatalf("match %+v err %v", msg.SenderAllowed, err)
	}
	mail.messages[0].From = "other@elsewhere.test"
	msg, err = svc.Get(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m")
	if err != nil || msg.SenderAllowed == nil || *msg.SenderAllowed {
		t.Fatalf("miss %+v err %v", msg.SenderAllowed, err)
	}
	list, set, err := svc.Allowlist(context.Background(), Caller{Agent: "ada"}, "inbox-a")
	if err != nil || !set || len(list) != 1 || list[0] != "example.com" {
		t.Fatalf("list %+v set %v err %v", list, set, err)
	}
	if _, _, err := svc.Allowlist(context.Background(), Caller{Agent: "bea"}, "inbox-a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger read err %v", err)
	}
	if err := svc.SetAllowlist(context.Background(), Caller{Operator: true}, "inbox-a", nil); err != nil {
		t.Fatal(err)
	}
	msg, err = svc.Get(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m")
	if err != nil || msg.SenderAllowed != nil {
		t.Fatalf("cleared %+v err %v", msg.SenderAllowed, err)
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

func TestProvisionGrantCreatesAnInbox(t *testing.T) {
	mail := &fakeMail{created: "id-2"}
	svc := New(mail)
	svc.Allow("ada", "inbox-a", PermRead)
	if _, err := svc.CreateInbox(context.Background(), Caller{Agent: "ada"}, mailbackend.InboxSpec{Local: "bob"}, ""); !errors.Is(err, ErrForbidden) || mail.creates != 0 {
		t.Fatalf("read grant created err %v calls %d", err, mail.creates)
	}
	if err := svc.Grant(Caller{Operator: true}, "ada", "inbox-a", PermProvision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("scope err %v", err)
	}
	if err := svc.Grant(Caller{Operator: true}, "ada", "*", PermProvision); err != nil {
		t.Fatal(err)
	}
	id, err := svc.CreateInbox(context.Background(), Caller{Agent: "ada"}, mailbackend.InboxSpec{Local: "ada"}, "")
	if err != nil || id != "id-2" || mail.creates != 1 {
		t.Fatalf("create %s calls %d err %v", id, mail.creates, err)
	}
	if _, err := svc.Get(context.Background(), Caller{Agent: "ada"}, "inbox-b", "m"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("provision read err %v", err)
	}
	got, err := svc.Generate(context.Background(), Caller{Agent: "ada"}, GenerateRequest{
		Prefix: "p", Domain: "example.com", DomainID: "dom", RoleID: "role", Count: 1, Wait: true,
	}, "")
	if err != nil || len(got.Inboxes) != 1 || got.Inboxes[0].Address != "p00000@example.com" {
		t.Fatalf("generate %+v err %v", got, err)
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

func TestWorkspaceQuotaAndRecipientDomainRateLimit(t *testing.T) {
	mail := &fakeMail{}
	svc := New(mail)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	svc.SetSendLimits(10, 10, 3, 2)

	// Two sends to target.test succeed; third in same minute hits recipient-domain rate limit.
	m1 := mailbackend.Outbound{AccountID: "inbox-a", From: "a@dom1.test", To: "u1@target.test", Subject: "1"}
	m2 := mailbackend.Outbound{AccountID: "inbox-b", From: "b@dom2.test", To: "u2@target.test", Subject: "2"}
	m3 := mailbackend.Outbound{AccountID: "inbox-c", From: "c@dom3.test", To: "u3@target.test", Subject: "3"}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, m1, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, m2, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, m3, ""); !errors.Is(err, ErrQuota) {
		t.Fatalf("recipient rate limit err = %v", err)
	}

	// A different recipient domain still works in the same minute, consuming the 3rd workspace send.
	mOther := mailbackend.Outbound{AccountID: "inbox-c", From: "c@dom3.test", To: "u@other.test", Subject: "other"}
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, mOther, ""); err != nil {
		t.Fatal(err)
	}

	// Advancing to the next minute clears recipient rate limit, but workspace daily quota (3) still blocks.
	now = now.Add(time.Minute)
	if err := svc.Deliver(context.Background(), Caller{Operator: true}, m3, ""); !errors.Is(err, ErrQuota) {
		t.Fatalf("workspace daily quota err = %v", err)
	}
	if mail.sent != 3 {
		t.Fatalf("backend sends = %d, want 3", mail.sent)
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

func TestGenerateRequiresAnOperatorAndBatches(t *testing.T) {
	mail := &fakeMail{}
	svc := New(mail)
	_, err := svc.Generate(context.Background(), Caller{Agent: "ada"}, GenerateRequest{
		Prefix: "u", Domain: "example.com", DomainID: "dom", RoleID: "role", Count: 2, Wait: true,
	}, "")
	if !errors.Is(err, ErrForbidden) || len(mail.batches) != 0 {
		t.Fatalf("err %v batches %v", err, mail.batches)
	}
	got, err := svc.Generate(context.Background(), Caller{Operator: true}, GenerateRequest{
		Prefix: "u", Domain: "example.com", DomainID: "dom", RoleID: "role", Count: 2, Wait: true,
	}, "gen-1")
	if err != nil || len(got.Inboxes) != 2 || got.Inboxes[0].Address != "u00000@example.com" {
		t.Fatalf("generate %+v %v", got, err)
	}
	again, err := svc.Generate(context.Background(), Caller{Operator: true}, GenerateRequest{
		Prefix: "u", Domain: "example.com", DomainID: "dom", RoleID: "role", Count: 2, Wait: true,
	}, "gen-1")
	if err != nil || again.Inboxes[0].ID != got.Inboxes[0].ID || len(mail.batches) != 1 {
		t.Fatalf("replay %+v batches %v err %v", again, mail.batches, err)
	}
}

func TestGenerateAboveTheLimitReturnsAJob(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mail := &fakeMail{started: started, release: release}
	svc := New(mail)
	svc.SetSyncLimit(1)
	got, err := svc.Generate(context.Background(), Caller{Operator: true}, GenerateRequest{
		Prefix: "u", Domain: "example.com", DomainID: "dom", RoleID: "role", Count: 2,
	}, "")
	if err != nil || got.JobID == "" {
		t.Fatalf("job %+v %v", got, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	job, err := svc.Job(got.JobID)
	if err != nil || job.Status != "running" {
		t.Fatalf("running %+v %v", job, err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, err = svc.Job(got.JobID)
		if err == nil && job.Status == "done" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if job.Status != "done" || job.Created != 2 {
		t.Fatalf("finished %+v", job)
	}
}

func TestReplyAndDeleteUseTheirOwnGrants(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{
		ID: "m", Subject: "hello", MessageID: []string{"<a@example.com>"},
	}}}
	svc := New(mail)
	svc.Allow("ada", "inbox-a", PermRead)
	if _, _, err := svc.Search(context.Background(), Caller{Agent: "ada"}, "inbox-b", "hello", mailbackend.Page{}); !errors.Is(err, ErrForbidden) || mail.listed {
		t.Fatalf("search err %v listed %v", err, mail.listed)
	}
	if err := svc.Delete(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m"); !errors.Is(err, ErrForbidden) || mail.deleted {
		t.Fatalf("delete err %v deleted %v", err, mail.deleted)
	}
	reply := mailbackend.Outbound{From: "a@example.com", To: "b@example.com"}
	if err := svc.Reply(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m", reply, ""); !errors.Is(err, ErrForbidden) || mail.got {
		t.Fatalf("reply err %v got %v", err, mail.got)
	}
	svc.Allow("ada", "inbox-a", PermReply)
	if err := svc.Reply(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m", reply, ""); err != nil {
		t.Fatal(err)
	}
	if mail.last.Subject != "Re: hello" || len(mail.last.InReplyTo) != 1 || mail.last.InReplyTo[0] != "<a@example.com>" {
		t.Fatalf("reply %+v", mail.last)
	}
	svc.Allow("ada", "inbox-a", PermDelete)
	if err := svc.Delete(context.Background(), Caller{Agent: "ada"}, "inbox-b", "m"); !errors.Is(err, ErrForbidden) || mail.deleted {
		t.Fatalf("other inbox err %v deleted %v", err, mail.deleted)
	}
	if err := svc.Delete(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m"); err != nil || !mail.deleted {
		t.Fatalf("delete err %v deleted %v", err, mail.deleted)
	}
}

func TestInboxDirectoryIsOperatorOnly(t *testing.T) {
	mail := &fakeMail{}
	svc := New(mail)
	if _, _, err := svc.ListInboxes(context.Background(), Caller{Agent: "ada"}, "example.com", mailbackend.Page{}); !errors.Is(err, ErrForbidden) || mail.dir {
		t.Fatalf("list err %v called %v", err, mail.dir)
	}
	if err := svc.DeleteInbox(context.Background(), Caller{Agent: "ada"}, "alice-id"); !errors.Is(err, ErrForbidden) || mail.dir {
		t.Fatalf("delete err %v called %v", err, mail.dir)
	}
	got, _, err := svc.ListInboxes(context.Background(), Caller{Operator: true}, "example.com", mailbackend.Page{})
	if err != nil || len(got) != 1 || got[0].Address != "alice@example.com" || !mail.dir {
		t.Fatalf("list %+v err %v", got, err)
	}
}

func TestAttachmentRequiresTheReadGrant(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{
		ID: "m", Attachments: []mailbackend.Attachment{{Name: "note.txt", Type: "text/plain", BlobID: "blob-1"}},
	}}}
	svc := New(mail)
	if _, _, err := svc.Attachment(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m", "blob-1"); !errors.Is(err, ErrForbidden) || mail.got {
		t.Fatalf("err %v got %v", err, mail.got)
	}
	svc.Allow("ada", "inbox-a", PermRead)
	data, meta, err := svc.Attachment(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m", "blob-1")
	if err != nil || string(data) != "file-bytes" || meta.Name != "note.txt" {
		t.Fatalf("data %q meta %+v err %v", data, meta, err)
	}
	if _, _, err := svc.Attachment(context.Background(), Caller{Agent: "ada"}, "inbox-a", "m", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing %v", err)
	}
}

type fakeMail struct {
	created  string
	creates  int
	messages []mailbackend.Message
	listed   bool
	sent     int
	sendErr  error
	got      bool
	deleted  bool
	removed  []string
	dir      bool
	last     mailbackend.Outbound
	batches  []int
	started  chan struct{}
	release  chan struct{}
}

func (f *fakeMail) CreateInbox(context.Context, mailbackend.InboxSpec) (string, error) {
	f.creates++
	return f.created, nil
}

func (f *fakeMail) CreateInboxes(_ context.Context, specs []mailbackend.InboxSpec) ([]string, error) {
	if f.started != nil {
		select {
		case <-f.started:
		default:
			close(f.started)
		}
	}
	if f.release != nil {
		<-f.release
	}
	f.batches = append(f.batches, len(specs))
	ids := make([]string, len(specs))
	for i, spec := range specs {
		ids[i] = spec.Local
	}
	return ids, nil
}

func (f *fakeMail) Deliver(_ context.Context, msg mailbackend.Outbound) error {
	f.sent++
	f.last = msg
	return f.sendErr
}

func (f *fakeMail) List(_ context.Context, _ string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	f.listed = true
	return mailbackend.Slice(f.messages, func(m mailbackend.Message) string { return m.ID }, page)
}

func (f *fakeMail) Search(_ context.Context, _, _ string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	f.listed = true
	return mailbackend.Slice(f.messages, func(m mailbackend.Message) string { return m.ID }, page)
}

func (f *fakeMail) Get(context.Context, string, string) (mailbackend.Message, error) {
	f.got = true
	if len(f.messages) == 0 {
		return mailbackend.Message{}, nil
	}
	return f.messages[0], nil
}

func (f *fakeMail) Delete(_ context.Context, _, id string) error {
	f.deleted = true
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeMail) ListInboxes(_ context.Context, page mailbackend.Page) ([]mailbackend.Account, string, error) {
	f.dir = true
	return []mailbackend.Account{{ID: "alice-id", Local: "alice"}}, "", nil
}

func (f *fakeMail) DeleteInbox(context.Context, string) error {
	f.dir = true
	return nil
}

func (f *fakeMail) Download(context.Context, string, string) ([]byte, error) {
	f.got = true
	return []byte("file-bytes"), nil
}

func TestRevokeRemovesTheGrant(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "hello"}}}
	svc := New(mail)
	if err := svc.Grant(Caller{Operator: true}, "ada", "inbox-a", PermRead); err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(Caller{Agent: "ada"}, "ada", "inbox-a", PermRead); !errors.Is(err, ErrForbidden) || mail.listed {
		t.Fatalf("agent revoke err %v listed %v", err, mail.listed)
	}
	if err := svc.Revoke(Caller{Operator: true}, "ada", "inbox-a", PermRead); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a", mailbackend.Page{}); !errors.Is(err, ErrForbidden) || mail.listed {
		t.Fatalf("after revoke err %v listed %v", err, mail.listed)
	}
	rows, _, err := svc.Audit(context.Background(), Caller{Agent: "ada"}, 0, 0)
	if !errors.Is(err, ErrForbidden) || len(rows) != 0 {
		t.Fatalf("agent audit rows %d err %v", len(rows), err)
	}
	rows, _, err = svc.Audit(context.Background(), Caller{Operator: true}, 0, 0)
	if err != nil || len(rows) != 2 || rows[0].Action != "grant" || rows[1].Action != "revoke" {
		t.Fatalf("audit %+v err %v", rows, err)
	}
	if rows[0].Target != "ada inbox-a read" || rows[0].Actor != "operator" {
		t.Fatalf("grant row %+v", rows[0])
	}
}

func TestListReturnsTheNextCursor(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "a", Subject: "one"}, {ID: "b", Subject: "two"}}}
	svc := New(mail)
	svc.Allow("ada", "inbox-a", PermRead)
	first, next, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a", mailbackend.Page{Limit: 1})
	if err != nil || len(first) != 1 || first[0].ID != "a" || next != "a" {
		t.Fatalf("first %+v next %s err %v", first, next, err)
	}
	second, next, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a", mailbackend.Page{After: next, Limit: 1})
	if err != nil || len(second) != 1 || second[0].ID != "b" || next != "" {
		t.Fatalf("second %+v next %s err %v", second, next, err)
	}
	if _, _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a", mailbackend.Page{After: "gone"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown cursor %v", err)
	}
	if _, _, err := svc.List(context.Background(), Caller{Agent: "ada"}, "inbox-a", mailbackend.Page{Limit: 1000}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("limit err %v", err)
	}
}
