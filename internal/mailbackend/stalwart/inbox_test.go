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
		case "Mailbox/query":
			if args[0]["accountId"] != "alice-id" {
				t.Errorf("mailbox account %v", args[0]["accountId"])
			}
			writeMethods(w, []any{[]any{"Mailbox/query", map[string]any{"ids": []string{"box"}}, "q"}})
		case "Email/set":
			if len(names) != 2 || names[1] != "EmailSubmission/set" {
				t.Fatalf("send calls %v", names)
			}
			email := args[0]["create"].(map[string]any)["m"].(map[string]any)
			if email["subject"] != "hello" {
				t.Errorf("subject %v", email["subject"])
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
	want := "x:Account/set,Identity/get,Mailbox/query,Email/set,EmailSubmission/set,Email/query,Email/get"
	if strings.Join(methods, ",") != want {
		t.Fatalf("methods %v", methods)
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
