// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
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

func TestWaitReturnsMessagesThatAreAlreadyThere(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "hello"}}}
	box := ServiceBox{Service: service.New(mail), Caller: service.Caller{Operator: true}}
	start := time.Now()
	got, _ := Dispatch(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait_for_message","arguments":{"inbox":"inbox-a","timeoutSeconds":30}}}`), box)
	if time.Since(start) > time.Second || !strings.Contains(string(got), "hello") {
		t.Fatalf("wait %s after %s", got, time.Since(start))
	}
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
func (f *fakeMail) List(context.Context, string) ([]mailbackend.Message, error) {
	f.listed = true
	return f.messages, nil
}
func (f *fakeMail) Search(context.Context, string, string) ([]mailbackend.Message, error) {
	f.listed = true
	return f.messages, nil
}
func (f *fakeMail) Get(context.Context, string, string) (mailbackend.Message, error) {
	f.listed = true
	if len(f.messages) == 0 {
		return mailbackend.Message{}, nil
	}
	return f.messages[0], nil
}
func (f *fakeMail) Delete(context.Context, string, string) error { return nil }
