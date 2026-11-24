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

func TestCreateInboxesBatchesAccountSet(t *testing.T) {
	previous := accountBatch
	accountBatch = 2
	t.Cleanup(func() { accountBatch = previous })
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, args := allCalls(t, r.Body)
		create := args[0]["create"].(map[string]any)
		sizes = append(sizes, len(create))
		made := map[string]any{}
		for key := range create {
			made[key] = map[string]any{"id": "id-" + key}
		}
		writeMethods(w, []any{[]any{"x:Account/set", map[string]any{"created": made}, "b"}})
	}))
	defer srv.Close()
	ids, err := CreateInboxes(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, []InboxInput{
		{DomainID: "dom", RoleID: "role", Local: "u00000"},
		{DomainID: "dom", RoleID: "role", Local: "u00001"},
		{DomainID: "dom", RoleID: "role", Local: "u00002"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "id-u0,id-u1,id-u0" || strings.Join(ints(sizes), ",") != "2,1" {
		t.Fatalf("ids %v sizes %v", ids, sizes)
	}
}

func TestDeliverUploadsAttachments(t *testing.T) {
	var uploaded string
	var attached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/jmap/upload/") {
			if !strings.HasSuffix(r.URL.Path, "/alice-id") || r.Header.Get("Content-Type") != "text/plain" {
				t.Errorf("upload %s %s", r.URL.Path, r.Header.Get("Content-Type"))
			}
			raw, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			uploaded = string(raw)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"blobId":"blob-1","type":"text/plain","size":4}`))
			return
		}
		names, args := allCalls(t, r.Body)
		switch names[0] {
		case "Identity/get":
			writeMethods(w, []any{[]any{"Identity/get", map[string]any{"list": []any{map[string]any{"id": "ident", "email": "alice@example.com"}}}, "i"}})
		case "Mailbox/get":
			writeMethods(w, []any{[]any{"Mailbox/get", map[string]any{"list": []any{
				map[string]any{"id": "inbox-box", "role": "inbox"},
				map[string]any{"id": "drafts-box", "role": "drafts"},
				map[string]any{"id": "sent-box", "role": "sent"},
			}}, "m"}})
		case "Email/set":
			create := args[0]["create"].(map[string]any)["m"].(map[string]any)
			files, _ := create["attachments"].([]any)
			if len(files) == 1 {
				file := files[0].(map[string]any)
				attached = file["blobId"] == "blob-1" && file["name"] == "note.txt"
			}
			writeMethods(w, []any{
				[]any{"Email/set", map[string]any{"created": map[string]any{"m": map[string]any{"id": "msg"}}}, "e"},
				[]any{"EmailSubmission/set", map[string]any{"created": map[string]any{"s": map[string]any{"id": "sub"}}}, "s"},
			})
		default:
			t.Errorf("method %s", names[0])
		}
	}))
	defer srv.Close()
	err := Deliver(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, Delivery{
		AccountID: "alice-id", From: "alice@example.com", To: "bob@example.com",
		Subject: "files", Body: "see attached",
		Attachments: []mailbackend.File{{Name: "note.txt", Type: "text/plain", Data: []byte("note")}},
	})
	if err != nil || uploaded != "note" || !attached {
		t.Fatalf("deliver %v uploaded %q attached %v", err, uploaded, attached)
	}
}

func TestCreateInboxesSkipsAddressesThatExist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMethods(w, []any{[]any{"x:Account/set", map[string]any{
			"created": map[string]any{"u0": map[string]any{"id": "new-0"}, "u2": map[string]any{"id": "new-2"}},
			"notCreated": map[string]any{"u1": map[string]any{
				"type": "primaryKeyViolation", "properties": []string{"email"}, "objectId": map[string]any{"object": "Account", "id": "old"},
			}},
		}, "b"}})
	}))
	defer srv.Close()
	ids, err := CreateInboxes(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, []InboxInput{
		{DomainID: "dom", RoleID: "role", Local: "u00000"},
		{DomainID: "dom", RoleID: "role", Local: "u00001"},
		{DomainID: "dom", RoleID: "role", Local: "u00002"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "new-0,,new-2" {
		t.Fatalf("ids %q, want the existing account left out", ids)
	}
}

func TestCreateInboxesFailsOnAnotherRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMethods(w, []any{[]any{"x:Account/set", map[string]any{
			"notCreated": map[string]any{"u0": map[string]any{"type": "overQuota", "objectId": map[string]any{"id": "x"}}},
		}, "b"}})
	}))
	defer srv.Close()
	ids, err := CreateInboxes(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, []InboxInput{
		{DomainID: "dom", RoleID: "role", Local: "u00000"},
	})
	if err == nil || ids != nil {
		t.Fatalf("ids %v err %v", ids, err)
	}
}

func TestMailCreatesInboxesInItsDomainAndRole(t *testing.T) {
	var accounts []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		if len(names) != 1 || names[0] != "x:Account/set" {
			t.Errorf("methods %v", names)
		}
		create := args[0]["create"].(map[string]any)
		made := map[string]any{}
		for key, account := range create {
			accounts = append(accounts, account.(map[string]any))
			made[key] = map[string]any{"id": "id-" + key}
		}
		call := "b"
		if _, single := create["a"]; single {
			call = "a"
		}
		writeMethods(w, []any{[]any{"x:Account/set", map[string]any{"created": made}, call}})
	}))
	defer srv.Close()
	mail := Mail{Client: &Client{BaseURL: srv.URL, APIKey: "key"}, DomainID: "dom", RoleID: "inbox-role"}
	ctx := context.Background()
	if _, err := mail.CreateInbox(ctx, mailbackend.InboxSpec{Local: "alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mail.CreateInboxes(ctx, []mailbackend.InboxSpec{{Local: "u00000"}, {Local: "u00001"}}); err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("accounts %v", accounts)
	}
	for _, account := range accounts {
		roles := account["roles"].(map[string]any)["roleIds"].(map[string]any)
		if account["domainId"] != "dom" || len(roles) != 1 || roles["inbox-role"] != true {
			t.Errorf("account %v", account)
		}
	}
	if domain, role := mail.InboxPlacement(); domain != "dom" || role != "inbox-role" {
		t.Errorf("placement %s %s", domain, role)
	}

	unplaced := Mail{Client: &Client{BaseURL: srv.URL, APIKey: "key"}}
	if _, err := unplaced.CreateInbox(ctx, mailbackend.InboxSpec{Local: "bob"}); err == nil {
		t.Error("create without a domain and role succeeded")
	}
	if _, err := unplaced.CreateInboxes(ctx, []mailbackend.InboxSpec{{Local: "bob"}}); err == nil {
		t.Error("bulk create without a domain and role succeeded")
	}
	if len(accounts) != 3 {
		t.Errorf("Stalwart called without a domain and role: %v", accounts)
	}
}

func ints(values []int) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(rune('0' + value))
	}
	return out
}
