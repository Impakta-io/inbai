// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestParseZoneReadsMailRecords(t *testing.T) {
	zone := strings.Join([]string{
		"$ORIGIN example.org.",
		"example.org. 3600 IN MX 10 mail.example.org.",
		`@ TXT "v=spf1 mx -all"`,
		`v1-ed25519-20260101._domainkey TXT ( "v=DKIM1; k=ed25519; " "p=abc" )`,
		`_dmarc.example.org. TXT "v=DMARC1; p=reject; rua=mailto:postmaster@example.org"`,
		"_imaps._tcp.example.org. SRV 0 1 993 mail.example.org.",
	}, "\n")
	records, err := ParseZone(zone)
	if err != nil {
		t.Fatal(err)
	}
	want := []Record{
		{Name: "example.org", Type: "MX", Value: "10 mail.example.org"},
		{Name: "example.org", Type: "TXT", Value: "v=spf1 mx -all"},
		{Name: "v1-ed25519-20260101._domainkey.example.org", Type: "TXT", Value: "v=DKIM1; k=ed25519; p=abc"},
		{Name: "_dmarc.example.org", Type: "TXT", Value: "v=DMARC1; p=reject; rua=mailto:postmaster@example.org"},
	}
	got := mailRecords(records)
	if len(got) != len(want) {
		t.Fatalf("records %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d = %+v", i, got[i])
		}
	}
}

func TestCheckReportsPublishedAndMissingRecords(t *testing.T) {
	records := []Record{
		{Name: "example.org", Type: "MX", Value: "10 mail.example.org"},
		{Name: "example.org", Type: "TXT", Value: "v=spf1 mx -all"},
		{Name: "_dmarc.example.org", Type: "TXT", Value: "v=DMARC1; p=reject"},
	}
	findings := Check(context.Background(), records, fakeDNS{
		mx: map[string][]*net.MX{
			"example.org": {{Host: "mail.example.org.", Pref: 10}},
		},
		txt: map[string][]string{
			"example.org": {"google-site-verification=abc", "v=spf1 mx -all"},
		},
	})
	if findings[0].Status != "published" || findings[1].Status != "published" || findings[2].Status != "not published" {
		t.Fatalf("findings %+v", findings)
	}
}

func mailRecords(records []Record) []Record {
	var out []Record
	for _, record := range records {
		if record.Type == "MX" || record.Type == "TXT" {
			out = append(out, record)
		}
	}
	return out
}

type fakeDNS struct {
	mx  map[string][]*net.MX
	txt map[string][]string
}

func (f fakeDNS) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if records, ok := f.mx[name]; ok {
		return records, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (f fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if records, ok := f.txt[name]; ok {
		return records, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
