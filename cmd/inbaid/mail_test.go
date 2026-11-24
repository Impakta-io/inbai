// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Impakta-io/inbai/internal/config"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
	"github.com/Impakta-io/inbai/internal/service"
)

func TestServeReservesTheAccountsInitCreated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Stalwart called for a reserved account: %s %s", r.Method, r.URL.Path)
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()
	svc := service.New(stalwart.Mail{Client: &stalwart.Client{BaseURL: srv.URL, APIKey: "key"}})
	reserveAccounts(svc, config.File{OperatorAccountID: "op-id", ServiceAccountID: "svc-id"})
	operator := service.Caller{Operator: true}
	for _, id := range []string{"op-id", "svc-id"} {
		if err := svc.DeleteInbox(context.Background(), operator, id); !errors.Is(err, service.ErrConflict) {
			t.Errorf("delete %s err %v", id, err)
		}
	}
	for _, local := range []string{stalwart.OperatorAccountName, stalwart.ServiceAccountName} {
		spec := mailbackend.InboxSpec{DomainID: "dom", RoleID: "role", Local: local}
		if _, err := svc.CreateInbox(context.Background(), operator, spec, ""); !errors.Is(err, service.ErrConflict) {
			t.Errorf("create %s err %v", local, err)
		}
	}
}

func TestServerMailUsesTheComposeAddress(t *testing.T) {
	cfg := config.File{StalwartURL: "https://127.0.0.1"}
	got, insecure := serverMail(cfg, func(key string) string {
		switch key {
		case "INBAI_STALWART_URL":
			return " https://stalwart "
		case "INBAI_TLS_INSECURE":
			return "1"
		default:
			return ""
		}
	})
	if got.StalwartURL != "https://stalwart" || !insecure {
		t.Fatalf("url %s insecure %v", got.StalwartURL, insecure)
	}
	got, insecure = serverMail(config.File{StalwartURL: "https://mail.example.com"}, func(string) string { return "" })
	if got.StalwartURL != "https://mail.example.com" || insecure {
		t.Fatalf("public url %s insecure %v", got.StalwartURL, insecure)
	}
}
