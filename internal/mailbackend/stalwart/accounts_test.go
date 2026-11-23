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

func TestListDeleteAndDownload(t *testing.T) {
	var pages int
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
			pages++
			if pages == 1 {
				ids := make([]string, 200)
				for i := range ids {
					ids[i] = "alice-id"
				}
				writeMethods(w, []any{[]any{"x:Account/query", map[string]any{"ids": ids}, "q"}})
				return
			}
			writeMethods(w, []any{[]any{"x:Account/query", map[string]any{"ids": []string{}}, "q"}})
		case "x:Account/get":
			list := make([]any, 200)
			for i := range list {
				list[i] = map[string]any{"id": "alice-id", "name": "alice"}
			}
			writeMethods(w, []any{[]any{"x:Account/get", map[string]any{"list": list}, "g"}})
		case "x:Account/set":
			destroy := args[0]["destroy"].([]any)
			if destroy[0] != "alice-id" {
				t.Errorf("destroy %v", destroy)
			}
			writeMethods(w, []any{[]any{"x:Account/set", map[string]any{"destroyed": []string{"alice-id"}}, "d"}})
		default:
			t.Errorf("method %s", names[0])
		}
	}))
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, APIKey: "key"}
	got, err := ListInboxes(context.Background(), client)
	if err != nil || len(got) != 200 || got[0].Local != "alice" || pages != 2 {
		t.Fatalf("list %+v pages %d err %v", got, pages, err)
	}
	if err := DeleteInbox(context.Background(), client, "alice-id"); err != nil {
		t.Fatal(err)
	}
	raw, err := client.download(context.Background(), "alice-id", "blob-1")
	if err != nil || string(raw) != "file-bytes" {
		t.Fatalf("download %q err %v", raw, err)
	}
}
