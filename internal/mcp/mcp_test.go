// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestToolsListAndAMissingGrantDoesNotRead(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "secret", Preview: "body"}}}
	box := ServiceBox{Service: service.New(mail), Caller: service.Caller{Agent: "ada"}}
	listed, ok := Dispatch(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), box)
	if !ok || !strings.Contains(string(listed), `"name":"list_messages"`) {
		t.Fatalf("list %s", listed)
	}
	denied, ok := Dispatch(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":"inbox-b"}}}`), box)
	if !ok || !strings.Contains(string(denied), `"isError":true`) || mail.listed {
		t.Fatalf("denied %s listed %v", denied, mail.listed)
	}
	initResp, _ := Dispatch(context.Background(), []byte(`{"jsonrpc":"2.0","id":3,"method":"initialize"}`), box)
	if !strings.Contains(string(initResp), ProtocolVersion) {
		t.Fatalf("initialize %s", initResp)
	}
	note, ok := Dispatch(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), box)
	if ok || note != nil {
		t.Fatalf("notification produced a response %s", note)
	}
}

func TestGrantedReadMarksTheBodyExternal(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "hello", Text: "body"}}}
	svc := service.New(mail)
	if err := svc.Grant(service.Caller{Operator: true}, "ada", "inbox-a", service.PermRead); err != nil {
		t.Fatal(err)
	}
	box := ServiceBox{Service: svc, Caller: service.Caller{Agent: "ada"}}
	got, _ := Dispatch(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_message","arguments":{"inbox":"inbox-a","messageId":"m"}}}`), box)
	if !strings.Contains(string(got), "untrusted external content") || !strings.Contains(string(got), "externalContent") || !strings.Contains(string(got), "hello") {
		t.Fatalf("get %s", got)
	}
}

func TestStdioAnswersRequestsAndSkipsNotifications(t *testing.T) {
	in := strings.NewReader("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n")
	var out bytes.Buffer
	if err := Serve(context.Background(), in, &out, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), `"jsonrpc"`) != 1 || !strings.Contains(out.String(), `"result":{}`) {
		t.Fatalf("stdio %s", out.String())
	}
}

func TestWaitWithoutACursorWaitsForTheNextEvent(t *testing.T) {
	log := &headLog{Log: &store.Memory{}, read: make(chan struct{})}
	box := feedBox(t, log)
	putEvents(t, log, "m-old-1", "m-old-2", "m-old-3")
	done := make(chan string, 1)
	go func() {
		got, _ := Dispatch(context.Background(), waitCall(`"timeoutSeconds":5`), box)
		done <- string(got)
	}()
	<-log.read
	select {
	case got := <-done:
		t.Fatalf("wait returned before a new event: %s", got)
	case <-time.After(50 * time.Millisecond):
	}
	putEvents(t, log, "m-new")
	got := <-done
	if strings.Contains(got, "m-old") || !strings.Contains(got, `\"id\":\"m-new\"`) || !strings.Contains(got, `\"after\":4`) {
		t.Fatalf("wait %s", got)
	}
}

func TestWaitWithACursorReturnsOnlyNewerEvents(t *testing.T) {
	log := &store.Memory{}
	box := feedBox(t, log)
	putEvents(t, log, "m-1", "m-2", "m-3", "m-4")
	start := time.Now()
	got, _ := Dispatch(context.Background(), waitCall(`"after":2,"timeoutSeconds":5`), box)
	if time.Since(start) > time.Second || strings.Contains(string(got), "m-1") || strings.Contains(string(got), "m-2") ||
		!strings.Contains(string(got), "m-3") || !strings.Contains(string(got), "m-4") || !strings.Contains(string(got), `\"after\":4`) {
		t.Fatalf("wait %s after %s", got, time.Since(start))
	}
	again, _ := Dispatch(context.Background(), waitCall(`"after":4,"timeoutSeconds":0.05`), box)
	if !strings.Contains(string(again), `\"messages\":[]`) || !strings.Contains(string(again), `\"after\":4`) {
		t.Fatalf("wait %s", again)
	}
}

func TestWaitTimeoutReturnsTheHeadCursor(t *testing.T) {
	log := &store.Memory{}
	box := feedBox(t, log)
	putEvents(t, log, "m-1", "m-2")
	got, _ := Dispatch(context.Background(), waitCall(`"timeoutSeconds":0.05`), box)
	if strings.Contains(string(got), `"isError"`) || !strings.Contains(string(got), `\"messages\":[]`) || !strings.Contains(string(got), `\"after\":2`) {
		t.Fatalf("wait %s", got)
	}
	bad, _ := Dispatch(context.Background(), waitCall(`"after":"2"`), box)
	if !strings.Contains(string(bad), `"isError":true`) || !strings.Contains(string(bad), "after must be an event id") {
		t.Fatalf("bad cursor %s", bad)
	}
}

func TestWaitNeedsAGrantAndTheEventFeed(t *testing.T) {
	log := &headLog{Log: &store.Memory{}, read: make(chan struct{})}
	svc := service.New(&fakeMail{})
	svc.SetFeed(events.New(log))
	denied, _ := Dispatch(context.Background(), waitCall(`"timeoutSeconds":0.05`), ServiceBox{Service: svc, Caller: service.Caller{Agent: "ada"}})
	if !strings.Contains(string(denied), `"isError":true`) || log.heads != 0 {
		t.Fatalf("denied %s heads %d", denied, log.heads)
	}
	box := ServiceBox{Service: service.New(&fakeMail{messages: []mailbackend.Message{{ID: "m"}}}), Caller: service.Caller{Operator: true}}
	got, _ := Dispatch(context.Background(), waitCall(`"timeoutSeconds":0.05`), box)
	if !strings.Contains(string(got), `"isError":true`) || !strings.Contains(string(got), "event store is not configured") {
		t.Fatalf("no feed %s", got)
	}
}

func waitCall(args string) []byte {
	return []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait_for_message","arguments":{"inbox":"inbox-a",` + args + `}}}`)
}

func feedBox(t *testing.T, log store.Log) ServiceBox {
	t.Helper()
	svc := service.New(&fakeMail{})
	svc.SetFeed(events.New(log))
	if err := svc.Grant(service.Caller{Operator: true}, "ada", "inbox-a", service.PermRead); err != nil {
		t.Fatal(err)
	}
	return ServiceBox{Service: svc, Caller: service.Caller{Agent: "ada"}}
}

func putEvents(t *testing.T, log store.Log, ids ...string) {
	t.Helper()
	records := make([]store.Record, len(ids))
	for i, id := range ids {
		records[i] = store.Record{MessageID: id, Kind: "created"}
	}
	if err := log.Put(context.Background(), "inbox-a", "", "", records); err != nil {
		t.Fatal(err)
	}
}

// headLog closes read after the first head lookup, so a test knows the waiter has its cursor.
type headLog struct {
	store.Log
	read  chan struct{}
	once  sync.Once
	heads int
}

func (h *headLog) Head(ctx context.Context, inbox string) (int64, error) {
	id, err := h.Log.Head(ctx, inbox)
	h.heads++
	h.once.Do(func() { close(h.read) })
	return id, err
}

type fakeMail struct {
	messages []mailbackend.Message
	listed   bool
}

func (f *fakeMail) CreateInbox(context.Context, mailbackend.InboxSpec) (string, error) {
	return "", nil
}
func (f *fakeMail) CreateInboxes(context.Context, []mailbackend.InboxSpec) ([]string, error) {
	return nil, nil
}
func (f *fakeMail) Deliver(context.Context, mailbackend.Outbound) error { return nil }
func (f *fakeMail) SenderAddresses(_ context.Context, account string) ([]string, error) {
	return []string{account + "@example.com"}, nil
}
func (f *fakeMail) List(context.Context, string, mailbackend.Page) ([]mailbackend.Message, string, error) {
	f.listed = true
	return f.messages, "", nil
}
func (f *fakeMail) Search(context.Context, string, string, mailbackend.Page) ([]mailbackend.Message, string, error) {
	f.listed = true
	return f.messages, "", nil
}
func (f *fakeMail) Get(context.Context, string, string) (mailbackend.Message, error) {
	f.listed = true
	if len(f.messages) == 0 {
		return mailbackend.Message{}, nil
	}
	return f.messages[0], nil
}
func (f *fakeMail) Delete(context.Context, string, string) error { return nil }
func (f *fakeMail) ListInboxes(context.Context, mailbackend.Page) ([]mailbackend.Account, string, error) {
	return nil, "", nil
}
func (f *fakeMail) DeleteInbox(context.Context, string) error { return nil }
func (f *fakeMail) Download(context.Context, string, string) ([]byte, error) {
	return nil, nil
}

func TestSendAndReplyLeaveFromAndTheReplyRecipientToTheService(t *testing.T) {
	box := &recordBox{}
	call := func(name, args string) string {
		got, _ := Dispatch(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":{`+args+`}}}`), box)
		return string(got)
	}
	if got := call("send_message", `"inbox":"inbox-a","to":"bob@example.com","subject":"hi"`); strings.Contains(got, `"isError"`) || box.sent.From != "" || box.sent.To != "bob@example.com" {
		t.Fatalf("send %s %+v", got, box.sent)
	}
	if got := call("send_message", `"inbox":"inbox-a","subject":"hi"`); !strings.Contains(got, "missing to") {
		t.Fatalf("send without to %s", got)
	}
	if got := call("reply_message", `"inbox":"inbox-a","messageId":"m","text":"thanks"`); strings.Contains(got, `"isError"`) || box.replied.From != "" || box.replied.To != "" || box.replied.Body != "thanks" {
		t.Fatalf("reply %s %+v", got, box.replied)
	}
	listed, _ := Dispatch(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`), box)
	if strings.Contains(string(listed), `"required":["inbox","from"`) || strings.Contains(string(listed), `"required":["inbox","messageId","from"`) {
		t.Fatalf("from is still required: %s", listed)
	}
}

// recordBox keeps the last message each tool passed on.
type recordBox struct {
	sent, replied mailbackend.Outbound
}

func (b *recordBox) List(context.Context, string, string) ([]mailbackend.Message, error) {
	return nil, nil
}
func (b *recordBox) Get(context.Context, string, string) (mailbackend.Message, error) {
	return mailbackend.Message{}, nil
}
func (b *recordBox) Send(_ context.Context, _ string, msg mailbackend.Outbound) error {
	b.sent = msg
	return nil
}
func (b *recordBox) Reply(_ context.Context, _, _ string, msg mailbackend.Outbound) error {
	b.replied = msg
	return nil
}
func (b *recordBox) Delete(context.Context, string, string) error { return nil }
func (b *recordBox) Wait(context.Context, string, *int64, time.Duration) ([]mailbackend.Message, int64, error) {
	return nil, 0, nil
}
