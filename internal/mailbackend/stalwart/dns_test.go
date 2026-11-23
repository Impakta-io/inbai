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

func TestZoneFileReadsTheDomainAndCreatesItWhenMissing(t *testing.T) {
	const zone = "example.com. MX 10 mail.example.com.\n"
	var sawBearer bool
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer shown-once" {
			sawBearer = true
		}
		name, args := decodeCall(t, r.Body)
		methods = append(methods, name)
		switch name {
		case "x:Domain/query":
			filter := args["filter"].(map[string]any)
			if filter["name"] != "example.com" {
				t.Errorf("filter %v", filter)
			}
			writeMethod(w, name, "q", map[string]any{"ids": []string{}})
		case "x:Domain/set":
			writeMethod(w, name, "d", map[string]any{"created": map[string]any{"d1": map[string]any{"id": "dom"}}})
		case "x:Domain/get":
			writeMethod(w, name, "g", map[string]any{"list": []any{
				map[string]any{"id": "dom", "name": "example.com", "dnsZoneFile": zone},
			}})
		default:
			t.Errorf("method %s", name)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	got, err := ZoneFile(context.Background(), &Client{BaseURL: srv.URL, APIKey: "shown-once"}, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != zone {
		t.Fatalf("zone %q", got)
	}
	if !sawBearer {
		t.Fatal("request did not use the API key")
	}
	if strings.Join(methods, ",") != "x:Domain/query,x:Domain/set,x:Domain/get" {
		t.Fatalf("methods %v", methods)
	}
}

func TestZoneFileUsesAnExistingDomain(t *testing.T) {
	const zone = "example.com. TXT \"v=spf1 mx -all\"\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, _ := decodeCall(t, r.Body)
		switch name {
		case "x:Domain/query":
			writeMethod(w, name, "q", map[string]any{"ids": []string{"dom"}})
		case "x:Domain/set":
			t.Error("created a domain that already exists")
		case "x:Domain/get":
			writeMethod(w, name, "g", map[string]any{"list": []any{
				map[string]any{"id": "dom", "name": "example.com", "dnsZoneFile": zone},
			}})
		default:
			t.Errorf("method %s", name)
		}
	}))
	defer srv.Close()

	got, err := ZoneFile(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != zone {
		t.Fatalf("zone %q", got)
	}
}

func TestZoneFileRejectsAnEmptyZone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, _ := decodeCall(t, r.Body)
		switch name {
		case "x:Domain/query":
			writeMethod(w, name, "q", map[string]any{"ids": []string{"dom"}})
		case "x:Domain/get":
			writeMethod(w, name, "g", map[string]any{"list": []any{
				map[string]any{"id": "dom", "name": "example.com", "dnsZoneFile": "  \n"},
			}})
		default:
			t.Errorf("method %s", name)
		}
	}))
	defer srv.Close()
	_, err := ZoneFile(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, "example.com")
	if err == nil || !strings.Contains(err.Error(), "zone file") {
		t.Fatalf("err = %v", err)
	}
}

func decodeCall(t *testing.T, body io.Reader) (string, map[string]any) {
	t.Helper()
	return oneCall(t, body)
}
