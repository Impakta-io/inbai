// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

func TestMailChangesReportsLostChanges(t *testing.T) {
	for problem, lost := range map[string]bool{"cannotCalculateChanges": true, "serverFail": false} {
		t.Run(problem, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				names, _ := allCalls(t, r.Body)
				if names[0] != "Email/changes" {
					t.Errorf("method %s", names[0])
				}
				writeMethods(w, []any{[]any{"error", map[string]any{"type": problem, "description": "state s0"}, "c"}})
			}))
			defer srv.Close()
			client := &Client{BaseURL: srv.URL, APIKey: "key"}
			_, err := Mail{Client: client}.MailChanges(context.Background(), "acct", "s0")
			if err == nil || errors.Is(err, mailbackend.ErrChangesLost) != lost {
				t.Fatalf("err %v, want lost %v", err, lost)
			}
			if !strings.Contains(err.Error(), "stalwart c: "+problem+": state s0") {
				t.Fatalf("err text %q", err.Error())
			}
		})
	}
}

var _ mailbackend.InboxChecker = Mail{}

func TestInboxExistsAsksForTheOneAccount(t *testing.T) {
	cases := map[string]struct {
		answer []any
		exists bool
		fails  bool
	}{
		"listed":    {answer: []any{"x:Account/get", map[string]any{"list": []any{map[string]any{"id": "e", "name": "ada"}}, "notFound": []any{}}, "a"}, exists: true},
		"not found": {answer: []any{"x:Account/get", map[string]any{"list": []any{}, "notFound": []any{"e"}}, "a"}},
		"silent":    {answer: []any{"x:Account/get", map[string]any{"list": []any{}}, "a"}, fails: true},
		"refused":   {answer: []any{"error", map[string]any{"type": "forbidden"}, "a"}, fails: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				names, args := allCalls(t, r.Body)
				ids, _ := args[0]["ids"].([]any)
				if len(names) != 1 || names[0] != "x:Account/get" || len(ids) != 1 || ids[0] != "e" {
					t.Errorf("calls %v args %v", names, args)
				}
				writeMethods(w, []any{tc.answer})
			}))
			defer srv.Close()
			exists, err := Mail{Client: &Client{BaseURL: srv.URL, APIKey: "key"}}.InboxExists(context.Background(), "e")
			if (err != nil) != tc.fails || exists != tc.exists {
				t.Fatalf("exists %v err %v", exists, err)
			}
		})
	}
}

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
				"newState": "s1", "hasMoreChanges": false, "created": []string{"m1", "m-sent"}, "destroyed": []any{},
			}, "c"}})
		case "Mailbox/query":
			if len(names) != 2 || names[1] != "Email/get" {
				t.Fatalf("expected batched Mailbox/query + Email/get, got %v", names)
			}
			writeMethods(w, []any{
				[]any{"Mailbox/query", map[string]any{"ids": []string{"inbox-box"}}, "b"},
				[]any{"Email/get", map[string]any{"list": []any{
					map[string]any{"id": "m1", "mailboxIds": map[string]bool{"inbox-box": true}},
					map[string]any{"id": "m-sent", "mailboxIds": map[string]bool{"sent-box": true}},
				}}, "g"},
			})
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
