// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
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
					if _, ok := args[0]["fetchTextBodyValues"]; !ok {
						t.Errorf("get did not ask for body values")
					}
				}
			}
			full := false
			sawAuth := false
			sawFrom := false
			for _, prop := range props {
				if prop == "bodyValues" {
					full = true
				}
				if prop == "header:Authentication-Results:asText" {
					sawAuth = true
				}
				if prop == "from" {
					sawFrom = true
				}
			}
			if full && !sawAuth {
				t.Error("get did not ask for authentication results")
			}
			if full && !sawFrom {
				t.Error("get did not ask for the sender")
			}
			writeMethods(w, []any{[]any{"Email/get", map[string]any{"list": []any{map[string]any{
				"id": "msg", "subject": "hello", "preview": "body",
				"messageId":                            []string{"<a@example.com>"},
				"textBody":                             []any{map[string]any{"partId": "p"}},
				"bodyValues":                           map[string]any{"p": map[string]any{"value": "body"}},
				"from":                                 []any{map[string]any{"email": "ada@example.com"}},
				"keywords":                             map[string]bool{"$junk": true},
				"header:Authentication-Results:asText": "mail.example.com; spf=pass; dkim=pass; dmarc=fail",
			}}}, "g"}})
		case "Mailbox/query":
			writeMethods(w, []any{[]any{"Mailbox/query", map[string]any{"ids": []string{"inbox-box"}}, "q"}})
		case "Email/query":
			filter, _ := args[0]["filter"].(map[string]any)
			if filter["inMailbox"] != "inbox-box" || filter["text"] != "hello" {
				t.Errorf("search filter %#v", filter)
			}
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
	if err != nil || got.Text != "body" || got.From != "ada@example.com" || len(got.MessageID) != 1 || got.Authentication.SPF != "pass" || got.Authentication.DMARC != "fail" || got.Authentication.Spam != "spam" {
		t.Fatalf("get %+v %v", got, err)
	}
	found, err := SearchMessages(context.Background(), client, "alice-id", "hello")
	if err != nil || len(found) != 1 {
		t.Fatalf("search %+v %v", found, err)
	}
	if err := DeleteMessage(context.Background(), client, "alice-id", "msg"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "Email/get,Mailbox/query,Email/query,Email/get,Email/set" {
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
		case "Mailbox/get":
			writeMethods(w, []any{[]any{"Mailbox/get", map[string]any{"list": []any{
				map[string]any{"id": "inbox-box", "role": "inbox"},
				map[string]any{"id": "drafts-box", "role": "drafts"},
				map[string]any{"id": "sent-box", "role": "sent"},
			}}, "m"}})
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

func TestDeleteMessageReportsARefusal(t *testing.T) {
	cases := map[string]struct {
		result  map[string]any
		want    string
		refused string
	}{
		"refused": {
			result:  map[string]any{"notDestroyed": map[string]any{"msg": map[string]any{"type": "forbidden", "description": "Read-only mailbox"}}},
			want:    "stalwart msg: forbidden: Read-only mailbox",
			refused: "forbidden",
		},
		"no result": {
			result: map[string]any{},
			want:   "missing destroy",
		},
		"already gone": {
			result: map[string]any{"notDestroyed": map[string]any{"msg": map[string]any{"type": "notFound"}}},
		},
		"destroyed": {
			result: map[string]any{"destroyed": []string{"msg"}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				names, _ := allCalls(t, r.Body)
				if names[0] != "Email/set" {
					t.Errorf("method %s", names[0])
				}
				writeMethods(w, []any{[]any{"Email/set", tc.result, "e"}})
			}))
			defer srv.Close()
			err := DeleteMessage(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, "alice-id", "msg")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("delete %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("delete error %v, want %q", err, tc.want)
			}
			var refused *mailbackend.RefusedError
			if got := errors.As(err, &refused); got != (tc.refused != "") || (got && (refused.Type != tc.refused || refused.Description == "")) {
				t.Fatalf("delete error %v refusal %+v, want type %q", err, refused, tc.refused)
			}
		})
	}
}
