// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetSearchAndDelete(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		methods = append(methods, names...)
		switch names[0] {
		case "Email/get":
			props, _ := args[0]["properties"].([]any)
			for _, prop := range props {
				if prop == "bodyValues" {
					if _, ok := args[0]["fetchAllBodyValues"]; !ok {
						t.Errorf("get did not ask for body values")
					}
				}
			}
			writeMethods(w, []any{[]any{"Email/get", map[string]any{"list": []any{map[string]any{
				"id": "msg", "subject": "hello", "preview": "body",
				"messageId":  []string{"<a@example.com>"},
				"textBody":   []any{map[string]any{"partId": "p"}},
				"bodyValues": map[string]any{"p": map[string]any{"value": "body"}},
			}}}, "g"}})
		case "Email/query":
			writeMethods(w, []any{[]any{"Email/query", map[string]any{"ids": []string{"msg"}}, "q"}})
		case "Email/set":
			destroy, _ := args[0]["destroy"].([]any)
			if len(destroy) != 1 || destroy[0] != "msg" {
				t.Errorf("destroy %#v", args[0]["destroy"])
			}
			writeMethods(w, []any{[]any{"Email/set", map[string]any{"destroyed": []string{"msg"}}, "e"}})
		default:
			t.Errorf("method %s", names[0])
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, APIKey: "key"}
	got, err := GetMessage(context.Background(), client, "alice-id", "msg")
	if err != nil || got.Text != "body" || len(got.MessageID) != 1 {
		t.Fatalf("get %+v %v", got, err)
	}
	found, err := SearchMessages(context.Background(), client, "alice-id", "hello")
	if err != nil || len(found) != 1 {
		t.Fatalf("search %+v %v", found, err)
	}
	if err := DeleteMessage(context.Background(), client, "alice-id", "msg"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "Email/get,Email/query,Email/get,Email/set" {
		t.Fatalf("methods %v", methods)
	}
}

func TestReplySetsThreadingHeaders(t *testing.T) {
	var sawReply bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		names, args := allCalls(t, strings.NewReader(string(body)))
		switch names[0] {
		case "Identity/get":
			writeMethods(w, []any{[]any{"Identity/get", map[string]any{"list": []any{map[string]any{"id": "ident"}}}, "i"}})
		case "Mailbox/query":
			writeMethods(w, []any{[]any{"Mailbox/query", map[string]any{"ids": []string{"box"}}, "q"}})
		case "Email/set":
			create := args[0]["create"].(map[string]any)["m"].(map[string]any)
			reply, _ := create["inReplyTo"].([]any)
			if len(reply) != 1 || reply[0] != "<a@example.com>" {
				t.Errorf("inReplyTo %#v", create["inReplyTo"])
			}
			sawReply = true
			writeMethods(w, []any{
				[]any{"Email/set", map[string]any{"created": map[string]any{"m": map[string]any{"id": "msg"}}}, "e"},
				[]any{"EmailSubmission/set", map[string]any{"created": map[string]any{"s": map[string]any{"id": "sub"}}}, "s"},
			})
		default:
			t.Errorf("%s", names[0])
		}
	}))
	defer srv.Close()
	err := Deliver(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, Delivery{
		AccountID: "alice-id", From: "alice@example.com", To: "bob@example.com",
		Subject: "Re: hello", Body: "reply", InReplyTo: []string{"<a@example.com>"},
	})
	if err != nil || !sawReply {
		t.Fatalf("deliver %v saw %v", err, sawReply)
	}
}
