// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMailChangesUsesTheStoredState(t *testing.T) {
	var sawSince string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		switch names[0] {
		case "Email/query":
			if args[0]["limit"] != float64(0) {
				t.Errorf("limit %#v", args[0]["limit"])
			}
			writeMethods(w, []any{[]any{"Email/query", map[string]any{"ids": []any{}, "queryState": "s0"}, "t"}})
		case "Email/changes":
			sawSince, _ = args[0]["sinceState"].(string)
			writeMethods(w, []any{[]any{"Email/changes", map[string]any{
				"newState": "s1", "hasMoreChanges": false, "created": []string{"m1"}, "destroyed": []any{},
			}, "c"}})
		default:
			t.Errorf("method %s", names[0])
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, APIKey: "key"}
	state, err := MailState(context.Background(), client, "acct")
	if err != nil || state != "s0" {
		t.Fatalf("state %s %v", state, err)
	}
	delta, err := MailChanges(context.Background(), client, "acct", state)
	if err != nil || delta.State != "s1" || len(delta.Created) != 1 || delta.Created[0] != "m1" || sawSince != "s0" {
		t.Fatalf("delta %+v since %s err %v", delta, sawSince, err)
	}
}
