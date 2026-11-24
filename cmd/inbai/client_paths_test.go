// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestAPIClientEscapesPathSegments(t *testing.T) {
	mux := http.NewServeMux()
	var got []string
	record := func(name string, values ...string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			got = []string{name}
			for _, v := range values {
				got = append(got, r.PathValue(v))
			}
			_, _ = w.Write([]byte(`{}`))
		}
	}
	mux.HandleFunc("DELETE /v1/inboxes/{id}", record("delete-inbox", "id"))
	mux.HandleFunc("DELETE /v1/inboxes/{id}/messages/{mid}", record("delete-message", "id", "mid"))
	mux.HandleFunc("GET /v1/inboxes/{id}/messages/{mid}", record("get-message", "id", "mid"))
	mux.HandleFunc("GET /v1/inboxes/{id}/messages/{mid}/attachments/{blob}", record("download", "id", "mid", "blob"))
	mux.HandleFunc("POST /v1/inboxes/{id}/messages/{mid}/reply", record("reply", "id", "mid"))
	mux.HandleFunc("POST /v1/inboxes/{id}/messages", record("send", "id"))
	mux.HandleFunc("GET /v1/inboxes/{id}/messages", record("list", "id"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := apiClient{base: srv.URL, token: "t"}
	ctx := context.Background()
	for _, id := range []string{"acct123?", "a/b", "a#b", "100%", "a%2Fb", "..", "x?y/../z#"} {
		cases := []struct {
			name string
			run  func() error
			want []string
		}{
			{"delete-message", func() error { return c.deleteMessage(ctx, id, id) }, []string{"delete-message", id, id}},
			{"delete-inbox", func() error { return c.deleteInbox(ctx, id) }, []string{"delete-inbox", id}},
			{"get-message", func() error { _, err := c.getMessage(ctx, id, id); return err }, []string{"get-message", id, id}},
			{"download", func() error { _, err := c.download(ctx, id, id, id); return err }, []string{"download", id, id, id}},
			{"reply", func() error { return c.reply(ctx, id, id, apiSend{}) }, []string{"reply", id, id}},
			{"send", func() error { return c.send(ctx, id, apiSend{}) }, []string{"send", id}},
			{"list", func() error { _, _, err := c.listMessages(ctx, id, "", "", 0); return err }, []string{"list", id}},
		}
		for _, tc := range cases {
			got = nil
			if err := tc.run(); err != nil {
				t.Errorf("%s id %q: %v", tc.name, id, err)
				continue
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("%s id %q: routed %q, want %q", tc.name, id, got, tc.want)
			}
		}
	}
}
