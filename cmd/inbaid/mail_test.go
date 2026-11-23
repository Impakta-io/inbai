// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/Impakta-io/inbai/internal/config"
)

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
