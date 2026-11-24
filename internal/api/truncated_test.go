// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

func TestGetMessageCarriesTextTruncated(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "big", Text: "abc...", TextTruncated: true}}}
	srv := httptest.NewServer((&Server{Service: service.New(mail), Token: "operator-token", Domain: "example.com"}).Handler())
	defer srv.Close()
	res := call(t, srv.URL, http.MethodGet, "/v1/inboxes/alice-id/messages/m", "operator-token", "", "")
	if body := readBody(t, res); !strings.Contains(body, `"textTruncated":true`) {
		t.Fatalf("cut message %s", body)
	}
}
