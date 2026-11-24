// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZoneFileReadsAndNeverCreatesAMissingDomain(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, args := decodeCall(t, r.Body)
		methods = append(methods, name)
		switch name {
		case "x:Domain/query":
			filter := args["filter"].(map[string]any)
			if filter["name"] != "gmail.com" {
				t.Errorf("filter %v", filter)
			}
			writeMethod(w, name, "q", map[string]any{"ids": []string{}})
		default:
			t.Errorf("method %s", name)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	_, err := ZoneFile(context.Background(), &Client{BaseURL: srv.URL, APIKey: "k"}, "gmail.com")
	if !errors.Is(err, ErrDomainNotConfigured) {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(methods, ",") != "x:Domain/query" {
		t.Fatalf("methods %v", methods)
	}
}

func TestCreateDomainCreatesOnlyTheNamedDomain(t *testing.T) {
	var created []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, args := decodeCall(t, r.Body)
		if name != "x:Domain/set" {
			t.Errorf("method %s", name)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		for _, item := range args["create"].(map[string]any) {
			created = append(created, item.(map[string]any)["name"].(string))
		}
		writeMethod(w, name, "d", map[string]any{"created": map[string]any{"d1": map[string]any{"id": "dom"}}})
	}))
	defer srv.Close()
	if err := CreateDomain(context.Background(), &Client{BaseURL: srv.URL, APIKey: "k"}, "example.com"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(created, ",") != "example.com" {
		t.Fatalf("created %v", created)
	}
	if err := CreateDomain(context.Background(), &Client{BaseURL: srv.URL, APIKey: "k"}, "nodot"); err == nil {
		t.Fatal("accepted a name that is not a DNS name")
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
