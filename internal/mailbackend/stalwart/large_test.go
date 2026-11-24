// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetMessageReadsResponseOverOneMiB(t *testing.T) {
	body := strings.Repeat("a", 2<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, args := allCalls(t, r.Body)
		if _, ok := args[0]["fetchAllBodyValues"]; ok {
			t.Error("get fetched every body value")
		}
		if args[0]["fetchTextBodyValues"] != true {
			t.Error("get did not ask for the text body")
		}
		if got, _ := args[0]["maxBodyValueBytes"].(float64); got != maxBodyValueBytes {
			t.Errorf("maxBodyValueBytes = %v", args[0]["maxBodyValueBytes"])
		}
		writeMethods(w, []any{[]any{"Email/get", map[string]any{"list": []any{map[string]any{
			"id": "msg", "subject": "big",
			"textBody":   []any{map[string]any{"partId": "p"}},
			"bodyValues": map[string]any{"p": map[string]any{"value": body, "isTruncated": true}},
		}}}, "g"}})
	}))
	defer srv.Close()
	got, err := GetMessage(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, "alice-id", "msg", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != body {
		t.Fatalf("text length %d, want %d", len(got.Text), len(body))
	}
}

func TestInvokeReportsResponseTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := []byte(strings.Repeat(" ", 1<<20))
		for i := 0; i <= maxJMAPResponse>>20; i++ {
			_, _ = w.Write(chunk)
		}
	}))
	defer srv.Close()
	_, err := GetMessage(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, "alice-id", "msg", "")
	if err == nil || !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("err = %v", err)
	}
}
