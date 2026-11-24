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

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

func TestListDeleteAndDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/jmap/download/") {
			if r.Header.Get("Authorization") != "Bearer key" {
				t.Errorf("auth %s", r.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(w, "file-bytes")
			return
		}
		names, args := allCalls(t, r.Body)
		switch names[0] {
		case "x:Account/query":
			if args[0]["limit"] != float64(2) {
				t.Errorf("limit %v", args[0]["limit"])
			}
			ids := []string{"alice-id", "bob-id"}
			if args[0]["anchor"] != nil {
				if args[0]["anchor"] != "alice-id" || args[0]["anchorOffset"] != float64(1) {
					t.Errorf("anchor %v offset %v", args[0]["anchor"], args[0]["anchorOffset"])
				}
				ids = []string{"bob-id"}
			}
			writeMethods(w, []any{[]any{"x:Account/query", map[string]any{"ids": ids}, "q"}})
		case "x:Account/get":
			asked := args[0]["ids"].([]any)
			list := make([]any, len(asked))
			for i, id := range asked {
				list[i] = map[string]any{"id": id, "name": "alice"}
			}
			writeMethods(w, []any{[]any{"x:Account/get", map[string]any{"list": list}, "g"}})
		case "x:Account/set":
			destroy := args[0]["destroy"].([]any)
			if destroy[0] != "alice-id" {
				t.Errorf("destroy %v", destroy)
			}
			writeMethods(w, []any{[]any{"x:Account/set", map[string]any{"destroyed": []string{"alice-id"}}, "d"}})
		case "Mailbox/query":
			writeMethods(w, []any{[]any{"Mailbox/query", map[string]any{"ids": []string{"inbox-box"}}, "q"}})
		case "Email/query":
			filter, _ := args[0]["filter"].(map[string]any)
			if args[0]["anchor"] != "m1" || args[0]["limit"] != float64(2) || filter["inMailbox"] != "inbox-box" {
				t.Errorf("email query %v", args[0])
			}
			writeMethods(w, []any{[]any{"Email/query", map[string]any{"ids": []string{"m2"}}, "q"}})
		case "Email/get":
			writeMethods(w, []any{[]any{"Email/get", map[string]any{"list": []any{map[string]any{"id": "m2", "subject": "next"}}}, "g"}})
		default:
			t.Errorf("method %s", names[0])
		}
	}))
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, APIKey: "key"}
	got, next, err := ListInboxes(context.Background(), client, mailbackend.Page{Limit: 1})
	if err != nil || len(got) != 1 || got[0].ID != "alice-id" || next != "alice-id" {
		t.Fatalf("list %+v next %s err %v", got, next, err)
	}
	rest, next, err := ListInboxes(context.Background(), client, mailbackend.Page{After: next, Limit: 1})
	if err != nil || len(rest) != 1 || rest[0].ID != "bob-id" || next != "" {
		t.Fatalf("rest %+v next %s err %v", rest, next, err)
	}
	found, next, err := queryMessages(context.Background(), client, "alice-id", nil, mailbackend.Page{After: "m1", Limit: 1}, false)
	if err != nil || len(found) != 1 || found[0].Subject != "next" || next != "" {
		t.Fatalf("messages %+v next %s err %v", found, next, err)
	}
	if err := DeleteInbox(context.Background(), client, "alice-id"); err != nil {
		t.Fatal(err)
	}
	raw, err := client.download(context.Background(), "alice-id", "blob-1")
	if err != nil || string(raw) != "file-bytes" {
		t.Fatalf("download %q err %v", raw, err)
	}
}
