// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateInboxAndReadADeliveredMessage(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		names, args := allCalls(t, r.Body)
		methods = append(methods, names...)
		switch names[0] {
		case "x:Account/set":
			account := args[0]["create"].(map[string]any)["a"].(map[string]any)
			if account["name"] != "alice" || account["domainId"] != "dom" {
				t.Errorf("account %v", account)
			}
			roles := account["roles"].(map[string]any)["roleIds"].(map[string]any)
			if roles["inbox-role"] != true {
				t.Errorf("roles %v", roles)
			}
			writeMethods(w, []any{[]any{"x:Account/set", map[string]any{"created": map[string]any{"a": map[string]any{"id": "alice-id"}}}, "a"}})
		case "Identity/get":
			writeMethods(w, []any{[]any{"Identity/get", map[string]any{"list": []any{map[string]any{"id": "ident"}}}, "i"}})
		case "Mailbox/get":
			if args[0]["accountId"] != "alice-id" {
				t.Errorf("mailbox account %v", args[0]["accountId"])
			}
			writeMethods(w, []any{[]any{"Mailbox/get", map[string]any{"list": []any{
				map[string]any{"id": "inbox-box", "role": "inbox"},
				map[string]any{"id": "drafts-box", "role": "drafts"},
				map[string]any{"id": "sent-box", "role": "sent"},
			}}, "m"}})
		case "Mailbox/query":
			if args[0]["accountId"] != "alice-id" {
				t.Errorf("mailbox account %v", args[0]["accountId"])
			}
			writeMethods(w, []any{[]any{"Mailbox/query", map[string]any{"ids": []string{"inbox-box"}}, "q"}})
		case "Email/set":
			if len(names) != 2 || names[1] != "EmailSubmission/set" {
				t.Fatalf("send calls %v", names)
			}
			email := args[0]["create"].(map[string]any)["m"].(map[string]any)
			if email["subject"] != "hello" {
				t.Errorf("subject %v", email["subject"])
			}
			boxes := email["mailboxIds"].(map[string]any)
			if boxes["sent-box"] != true {
				t.Errorf("sent mailboxIds %v", boxes)
			}
			keywords := email["keywords"].(map[string]any)
			if keywords["$seen"] != true || keywords["$draft"] != nil {
				t.Errorf("keywords %v", keywords)
			}
			submission := args[1]["create"].(map[string]any)["s"].(map[string]any)
			if submission["emailId"] != "#m" || submission["identityId"] != "ident" {
				t.Errorf("submission %v", submission)
			}
			writeMethods(w, []any{
				[]any{"Email/set", map[string]any{"created": map[string]any{"m": map[string]any{"id": "msg"}}}, "e"},
				[]any{"EmailSubmission/set", map[string]any{"created": map[string]any{"s": map[string]any{"id": "sub"}}}, "s"},
			})
		case "Email/query":
			filter, _ := args[0]["filter"].(map[string]any)
			if filter["inMailbox"] != "inbox-box" {
				t.Errorf("Email/query filter %#v", filter)
			}
			writeMethods(w, []any{[]any{"Email/query", map[string]any{"ids": []string{"msg"}}, "q"}})
		case "Email/get":
			writeMethods(w, []any{[]any{"Email/get", map[string]any{"list": []any{map[string]any{
				"id": "msg", "subject": "hello", "preview": "body",
			}}}, "g"}})
		default:
			t.Errorf("methods %v", names)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, APIKey: "key"}
	id, err := CreateInbox(context.Background(), client, InboxInput{DomainID: "dom", RoleID: "inbox-role", Local: "alice"})
	if err != nil || id != "alice-id" {
		t.Fatalf("create %s %v", id, err)
	}
	if err := Deliver(context.Background(), client, Delivery{
		AccountID: id,
		From:      "alice@example.com",
		To:        "bob@example.com",
		Subject:   "hello",
		Body:      "body",
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := ListMessages(context.Background(), client, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Subject != "hello" || messages[0].Preview != "body" {
		t.Fatalf("messages %+v", messages)
	}
	want := "x:Account/set,Identity/get,Mailbox/get,Email/set,EmailSubmission/set,Mailbox/query,Email/query,Email/get"
	if strings.Join(methods, ",") != want {
		t.Fatalf("methods %v", methods)
	}
}

func TestDeliverFallsBackToInboxWhenSentMailboxIsMissing(t *testing.T) {
	var usedInbox bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		switch names[0] {
		case "Identity/get":
			writeMethods(w, []any{[]any{"Identity/get", map[string]any{"list": []any{map[string]any{"id": "ident"}}}, "i"}})
		case "Mailbox/get":
			writeMethods(w, []any{[]any{"Mailbox/get", map[string]any{"list": []any{
				map[string]any{"id": "inbox-box", "role": "inbox"},
			}}, "m"}})
		case "Email/set":
			email := args[0]["create"].(map[string]any)["m"].(map[string]any)
			boxes := email["mailboxIds"].(map[string]any)
			usedInbox = boxes["inbox-box"] == true
			writeMethods(w, []any{
				[]any{"Email/set", map[string]any{"created": map[string]any{"m": map[string]any{"id": "msg"}}}, "e"},
				[]any{"EmailSubmission/set", map[string]any{"created": map[string]any{"s": map[string]any{"id": "sub"}}}, "s"},
			})
		default:
			t.Errorf("methods %v", names)
		}
	}))
	defer srv.Close()
	if err := Deliver(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, Delivery{
		AccountID: "alice-id", From: "alice@example.com", To: "bob@example.com", Subject: "hi", Body: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	if !usedInbox {
		t.Fatal("expected fallback to inbox mailbox when sent and drafts are missing")
	}
}

func allCalls(t *testing.T, body io.Reader) ([]string, []map[string]any) {
	t.Helper()
	var req struct {
		MethodCalls [][]json.RawMessage `json:"methodCalls"`
	}
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(req.MethodCalls))
	args := make([]map[string]any, len(req.MethodCalls))
	for i, call := range req.MethodCalls {
		if err := json.Unmarshal(call[0], &names[i]); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(call[1], &args[i]); err != nil {
			t.Fatal(err)
		}
	}
	return names, args
}

func writeMethods(w http.ResponseWriter, responses []any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"methodResponses": responses})
}

// deliverServer fakes Stalwart for Deliver. send answers the Email/set and
// EmailSubmission/set pair. destroy answers a cleanup Email/set destroy;
// destroyed records the ids Deliver asked to remove.
func deliverServer(t *testing.T, send []any, destroy map[string]any, destroyed *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		switch names[0] {
		case "Identity/get":
			writeMethods(w, []any{[]any{"Identity/get", map[string]any{"list": []any{map[string]any{"id": "ident"}}}, "i"}})
		case "Mailbox/get":
			writeMethods(w, []any{[]any{"Mailbox/get", map[string]any{"list": []any{
				map[string]any{"id": "sent-box", "role": "sent"},
			}}, "m"}})
		case "Email/set":
			ids, ok := args[0]["destroy"].([]any)
			if !ok {
				writeMethods(w, send)
				return
			}
			for _, id := range ids {
				*destroyed = append(*destroyed, id.(string))
			}
			writeMethods(w, []any{[]any{"Email/set", destroy, "e"}})
		default:
			t.Errorf("methods %v", names)
		}
	}))
}

func deliverTo(srv *httptest.Server) error {
	return Deliver(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, Delivery{
		AccountID: "alice-id", From: "alice@example.com", To: "bob@example.com", Subject: "hi", Body: "hi",
	})
}

var destroyedMsg = map[string]any{"destroyed": []string{"msg"}}

func TestDeliverFailsWhenStalwartDoesNotStoreTheMessage(t *testing.T) {
	var destroyed []string
	srv := deliverServer(t, []any{
		[]any{"Email/set", map[string]any{"notCreated": map[string]any{"m": map[string]any{
			"type": "overQuota", "description": "Quota exceeded",
		}}}, "e"},
		[]any{"EmailSubmission/set", map[string]any{"notCreated": map[string]any{"s": map[string]any{
			"type": "invalidProperties", "description": "emailId #m not found",
		}}}, "s"},
	}, destroyedMsg, &destroyed)
	defer srv.Close()
	err := deliverTo(srv)
	if err == nil || !strings.Contains(err.Error(), "overQuota: Quota exceeded") {
		t.Fatalf("deliver error %v", err)
	}
	if len(destroyed) != 0 {
		t.Fatalf("destroyed %v when nothing was stored", destroyed)
	}
}

func TestDeliverFailsAndRemovesTheCopyWhenSubmissionIsRefused(t *testing.T) {
	var destroyed []string
	srv := deliverServer(t, []any{
		[]any{"Email/set", map[string]any{"created": map[string]any{"m": map[string]any{"id": "msg"}}}, "e"},
		[]any{"EmailSubmission/set", map[string]any{"notCreated": map[string]any{"s": map[string]any{
			"type": "forbiddenFrom", "description": "Sender not allowed",
		}}}, "s"},
	}, destroyedMsg, &destroyed)
	defer srv.Close()
	err := deliverTo(srv)
	if err == nil || !strings.Contains(err.Error(), "forbiddenFrom: Sender not allowed") {
		t.Fatalf("deliver error %v", err)
	}
	if strings.Contains(err.Error(), "not removed") {
		t.Fatalf("deliver error %v reports a cleanup failure", err)
	}
	if strings.Join(destroyed, ",") != "msg" {
		t.Fatalf("destroyed %v, want the stored copy", destroyed)
	}
}

func TestDeliverKeepsTheSubmissionErrorWhenCleanupFails(t *testing.T) {
	var destroyed []string
	srv := deliverServer(t, []any{
		[]any{"Email/set", map[string]any{"created": map[string]any{"m": map[string]any{"id": "msg"}}}, "e"},
		[]any{"EmailSubmission/set", map[string]any{"notCreated": map[string]any{"s": map[string]any{
			"type": "forbiddenToSend",
		}}}, "s"},
	}, map[string]any{"notDestroyed": map[string]any{"msg": map[string]any{"type": "forbidden"}}}, &destroyed)
	defer srv.Close()
	err := deliverTo(srv)
	if err == nil || !strings.Contains(err.Error(), "stalwart s: forbiddenToSend") || !strings.Contains(err.Error(), "msg was not removed") {
		t.Fatalf("deliver error %v", err)
	}
}

func TestDeliverFailsWhenTheSubmissionResultIsMissing(t *testing.T) {
	var destroyed []string
	srv := deliverServer(t, []any{
		[]any{"Email/set", map[string]any{"created": map[string]any{"m": map[string]any{"id": "msg"}}}, "e"},
		[]any{"EmailSubmission/set", map[string]any{}, "s"},
	}, destroyedMsg, &destroyed)
	defer srv.Close()
	if err := deliverTo(srv); err == nil || !strings.Contains(err.Error(), "missing result") {
		t.Fatalf("deliver error %v", err)
	}
	if strings.Join(destroyed, ",") != "msg" {
		t.Fatalf("destroyed %v, want the stored copy", destroyed)
	}
}
