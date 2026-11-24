// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetMessageReportsATruncatedBody(t *testing.T) {
	for name, value := range map[string]map[string]any{
		"cut":    {"value": "abc...", "isTruncated": true},
		"whole":  {"value": "abc", "isTruncated": false},
		"absent": {"value": "abc"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeMethods(w, []any{[]any{"Email/get", map[string]any{"list": []any{map[string]any{
					"id": "msg", "subject": "s",
					"textBody":   []any{map[string]any{"partId": "p"}},
					"bodyValues": map[string]any{"p": value},
				}}}, "g"}})
			}))
			defer srv.Close()
			got, err := GetMessage(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, "alice-id", "msg", "")
			if err != nil {
				t.Fatal(err)
			}
			if want := name == "cut"; got.TextTruncated != want {
				t.Fatalf("TextTruncated = %v, want %v", got.TextTruncated, want)
			}
		})
	}
}
