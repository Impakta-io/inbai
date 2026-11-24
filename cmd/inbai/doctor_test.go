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

func TestDoctorRequiresConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "--config", filepath.Join(t.TempDir(), "missing.json")}, &stdout, &stderr, func(string) string { return "" })
	if code != 2 || !strings.Contains(stderr.String(), "init") {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
}

func TestDoctorPrintsFixesAndExitsUnhealthy(t *testing.T) {
	const zone = "example.com. MX 10 mail.example.com.\nexample.com. TXT \"v=spf1 mx -all\"\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	path := filepath.Join(t.TempDir(), "config.json")
	raw := []byte(`{"domain":"example.com","hostname":"mail.example.com","stalwartUrl":"` + srv.URL + `","apiKey":"shown-once"}` + "\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	previous := doctorProbe
	doctorProbe = refusedProbe{}
	t.Cleanup(func() { doctorProbe = previous })

	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "--config", path}, &stdout, &stderr, func(string) string { return "" })
	if code != 1 {
		t.Fatalf("exit %d stderr %s\n%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "Port 25 mail.example.com: fix: allow inbound TCP from the internet") {
		t.Fatalf("stdout %s", stdout.String())
	}
	stdout.Reset()
	code = run([]string{"doctor", "--json", "--config", path}, &stdout, &stderr, func(string) string { return "" })
	if code != 1 {
		t.Fatalf("json exit %d %s", code, stdout.String())
	}
	var printed struct {
		Healthy bool `json:"healthy"`
		Checks  []struct {
			OK   bool   `json:"ok"`
			Text string `json:"text"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &printed); err != nil {
		t.Fatalf("json %s", stdout.String())
	}
	if printed.Healthy {
		t.Fatal("json reported a healthy host")
	}
	found := false
	for _, check := range printed.Checks {
		if strings.Contains(check.Text, "Port 25") {
			found = true
		}
	}
	if !found {
		t.Fatalf("printed %+v", printed)
	}
}

type refusedProbe struct{}

func (refusedProbe) LookupMX(context.Context, string) ([]*net.MX, error) {
	return []*net.MX{{Host: "mail.example.com.", Pref: 10}}, nil
}

func (refusedProbe) LookupTXT(context.Context, string) ([]string, error) {
	return []string{"v=spf1 mx -all"}, nil
}

func (refusedProbe) LookupIP(_ context.Context, host string) ([]net.IP, error) {
	if host == "mail.example.com" {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func (refusedProbe) LookupAddr(context.Context, string) ([]string, error) {
	return []string{"mail.example.com."}, nil
}

func (refusedProbe) Dial(context.Context, string) error { return errRefused{} }

func (refusedProbe) TLS(context.Context, string, string) error { return nil }

type errRefused struct{}

func (errRefused) Error() string { return "connection refused" }

func TestDoctorReportsAMissingDomainWithoutCreatingIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := methodName(t, r.Body)
		if name != "x:Domain/query" {
			t.Errorf("doctor called %s", name)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		writeInit(w, name, "q", map[string]any{"ids": []string{}})
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	raw := []byte(`{"domain":"example.com","hostname":"mail.example.com","stalwartUrl":"` + srv.URL + `","apiKey":"shown-once"}` + "\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "--domain", "gmail.com", "--config", path}, &stdout, &stderr, func(string) string { return "" })
	if code != 1 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	want := "domain is not configured in Stalwart; fix: run inbai domain add --domain gmail.com"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout %s", stdout.String())
	}
}
