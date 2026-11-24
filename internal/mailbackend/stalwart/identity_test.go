// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

var _ mailbackend.Backend = Mail{}

// identityServer fakes Stalwart with two identities on alice-id: her own address and an alias.
// submitted records the identityId of each EmailSubmission/set create.
func identityServer(t *testing.T, submitted *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		switch names[0] {
		case "Identity/get":
			if args[0]["accountId"] != "alice-id" {
				t.Errorf("identity account %v", args[0]["accountId"])
			}
			writeMethods(w, []any{[]any{"Identity/get", map[string]any{"list": []any{
				map[string]any{"id": "own", "email": "alice@example.com"},
				map[string]any{"id": "no-email"},
				map[string]any{"id": "alias", "email": "Sales@Example.com"},
			}}, "i"}})
		case "Mailbox/get":
			writeMethods(w, []any{[]any{"Mailbox/get", map[string]any{"list": []any{
				map[string]any{"id": "sent-box", "role": "sent"},
			}}, "m"}})
		case "Email/set":
			submission := args[1]["create"].(map[string]any)["s"].(map[string]any)
			*submitted = append(*submitted, submission["identityId"].(string))
			writeMethods(w, []any{
				[]any{"Email/set", map[string]any{"created": map[string]any{"m": map[string]any{"id": "msg"}}}, "e"},
				[]any{"EmailSubmission/set", map[string]any{"created": map[string]any{"s": map[string]any{"id": "sub"}}}, "s"},
			})
		default:
			t.Errorf("methods %v", names)
		}
	}))
}

func TestSenderAddressesListsTheIdentities(t *testing.T) {
	var submitted []string
	srv := identityServer(t, &submitted)
	defer srv.Close()
	got, err := Mail{Client: &Client{BaseURL: srv.URL, APIKey: "key"}}.SenderAddresses(context.Background(), "alice-id")
	if err != nil || strings.Join(got, ",") != "alice@example.com,Sales@Example.com" {
		t.Fatalf("addresses %v err %v", got, err)
	}
}

func TestDeliverSubmitsWithTheIdentityThatMatchesTheSender(t *testing.T) {
	var submitted []string
	srv := identityServer(t, &submitted)
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, APIKey: "key"}
	for _, from := range []string{"sales@example.com", "alice@example.com"} {
		if err := Deliver(context.Background(), client, Delivery{AccountID: "alice-id", From: from, To: "bob@example.com", Subject: "hi"}); err != nil {
			t.Fatalf("deliver as %s: %v", from, err)
		}
	}
	if strings.Join(submitted, ",") != "alias,own" {
		t.Fatalf("identities %v, want alias,own", submitted)
	}
	err := Deliver(context.Background(), client, Delivery{AccountID: "alice-id", From: "mallory@example.com", To: "bob@example.com", Subject: "hi"})
	if err == nil || !strings.Contains(err.Error(), "no identity for mallory@example.com") {
		t.Fatalf("foreign sender err %v", err)
	}
	if len(submitted) != 2 {
		t.Fatalf("a foreign sender reached Email/set: %v", submitted)
	}
}

func TestGetMessageReadsReplyTo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		if names[0] != "Email/get" {
			t.Errorf("method %s", names[0])
		}
		asked := false
		props, _ := args[0]["properties"].([]any)
		for _, prop := range props {
			if prop == "replyTo" {
				asked = true
			}
		}
		if !asked {
			t.Error("get did not ask for replyTo")
		}
		writeMethods(w, []any{[]any{"Email/get", map[string]any{"list": []any{map[string]any{
			"id": "msg", "subject": "hello",
			"from":    []any{map[string]any{"email": "ada@example.com"}},
			"replyTo": []any{map[string]any{"name": "Help", "email": "help@example.com"}, map[string]any{"email": "other@example.com"}},
		}}}, "g"}})
	}))
	defer srv.Close()
	got, err := Mail{Client: &Client{BaseURL: srv.URL, APIKey: "key"}}.Get(context.Background(), "alice-id", "msg")
	if err != nil || got.From != "ada@example.com" || got.ReplyTo != "help@example.com" {
		t.Fatalf("get %+v err %v", got, err)
	}
}
