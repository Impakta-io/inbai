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

func TestStorageQuotaGetSetAndCreate(t *testing.T) {
	var createdQuota float64
	var updatedQuota any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		names, args := allCalls(t, strings.NewReader(string(body)))
		switch names[0] {
		case "x:Account/set":
			if create, ok := args[0]["create"].(map[string]any); ok {
				obj := create["a"].(map[string]any)
				q := obj["quotas"].(map[string]any)
				createdQuota = q["maxDiskQuota"].(float64)
				writeMethods(w, []any{[]any{"x:Account/set", map[string]any{
					"created": map[string]any{"a": map[string]any{"id": "acct-1"}},
				}, "a"}})
				return
			}
			upd := args[0]["update"].(map[string]any)["acct-1"].(map[string]any)
			updatedQuota = upd["quotas"]
			writeMethods(w, []any{[]any{"x:Account/set", map[string]any{
				"updated": map[string]any{"acct-1": nil},
			}, "q"}})
		case "x:Account/get":
			writeMethods(w, []any{[]any{"x:Account/get", map[string]any{
				"list": []any{map[string]any{
					"id":            "acct-1",
					"usedDiskQuota": 4096,
					"quotas":        map[string]any{"maxDiskQuota": 104857600},
				}},
			}, "q"}})
		default:
			t.Errorf("unexpected method %s", names[0])
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, APIKey: "key"}
	ctx := context.Background()

	id, err := CreateInbox(ctx, client, InboxInput{DomainID: "d", RoleID: "r", Local: "alice", QuotaBytes: 104857600})
	if err != nil || id != "acct-1" || int64(createdQuota) != 104857600 {
		t.Fatalf("create id=%s quota=%v err=%v", id, createdQuota, err)
	}
	got, err := GetStorageQuota(ctx, client, "acct-1")
	if err != nil || got.LimitBytes != 104857600 || got.UsedBytes != 4096 {
		t.Fatalf("get %+v err %v", got, err)
	}
	if err := SetStorageQuota(ctx, client, "acct-1", 209715200); err != nil {
		t.Fatal(err)
	}
	if q, ok := updatedQuota.(map[string]any); !ok || int64(q["maxDiskQuota"].(float64)) != 209715200 {
		t.Fatalf("updated quota %#v", updatedQuota)
	}
	if err := SetStorageQuota(ctx, client, "acct-1", 0); err != nil {
		t.Fatal(err)
	}
	if q, ok := updatedQuota.(map[string]any); !ok || len(q) != 0 {
		t.Fatalf("cleared quota %#v", updatedQuota)
	}
}
