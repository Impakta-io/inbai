// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDomainAddRequiresConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"domain", "add", "--config", filepath.Join(t.TempDir(), "missing.json")}, &stdout, &stderr, func(string) string { return "" })
	if code != 2 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "init") {
		t.Fatalf("stderr %s", stderr.String())
	}
}

func TestDomainAddPrintsZoneAndChecksDNS(t *testing.T) {
	const zone = "example.com. MX 10 mail.example.com.\nexample.com. TXT \"v=spf1 mx -all\"\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer shown-once" {
			t.Errorf("authorization %s", r.Header.Get("Authorization"))
		}
		name := methodName(t, r.Body)
		switch name {
		case "x:Domain/query":
			writeInit(w, name, "q", map[string]any{"ids": []string{"dom"}})
		case "x:Domain/get":
			writeInit(w, name, "g", map[string]any{"list": []any{
				map[string]any{"id": "dom", "name": "example.com", "dnsZoneFile": zone},
			}})
		default:
			t.Errorf("method %s", name)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := []byte(`{"domain":"example.com","hostname":"mail.example.com","stalwartUrl":"` + srv.URL + `","apiKey":"shown-once","retentionDays":30}` + "\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	previous := lookupDNS
	lookupDNS = emptyDNS{}
	t.Cleanup(func() { lookupDNS = previous })

	var stdout, stderr bytes.Buffer
	code := run([]string{"domain", "add", "--config", path}, &stdout, &stderr, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "MX 10 mail.example.com.") || !strings.Contains(stdout.String(), "not published") {
		t.Fatalf("stdout %s", stdout.String())
	}
	stdout.Reset()
	code = run([]string{"domain", "add", "--json", "--config", path}, &stdout, &stderr, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("json exit %d stderr %s", code, stderr.String())
	}
	var printed struct {
		Zone   string `json:"zone"`
		Checks []struct {
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &printed); err != nil {
		t.Fatalf("json %s", stdout.String())
	}
	found := false
	for _, check := range printed.Checks {
		if strings.Contains(check.Status, "not published") {
			found = true
		}
	}
	if !strings.Contains(printed.Zone, "MX 10 mail.example.com.") || !found {
		t.Fatalf("printed %+v", printed)
	}
}

type emptyDNS struct{}

func (emptyDNS) LookupMX(context.Context, string) ([]*net.MX, error) {
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}

func (emptyDNS) LookupTXT(context.Context, string) ([]string, error) {
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}
