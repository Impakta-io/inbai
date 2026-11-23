// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestRunReportsAReachableHost(t *testing.T) {
	lines := Run(context.Background(), []Record{
		{Name: "example.com", Type: "MX", Value: "10 mail.example.com"},
		{Name: "example.com", Type: "TXT", Value: "v=spf1 mx -all"},
		{Name: "selector._domainkey.example.com", Type: "TXT", Value: "v=DKIM1; k=ed25519; p=abc"},
		{Name: "_dmarc.example.com", Type: "TXT", Value: "v=DMARC1; p=reject"},
	}, "mail.example.com", healthyProbe())
	text := join(lines)
	for _, want := range []string{
		"DNS example.com MX: ok",
		"DNS example.com TXT: ok",
		"DNS selector._domainkey.example.com TXT: ok",
		"DNS _dmarc.example.com TXT: ok",
		"PTR 203.0.113.10: ok",
		"Port 25 mail.example.com: ok",
		"TLS mail.example.com:443: ok",
		"Blocklist 203.0.113.10 zen.spamhaus.org: ok",
		"Blocklist 203.0.113.10 bl.spamcop.net: ok",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if !Healthy(lines) {
		t.Fatalf("expected a healthy report:\n%s", text)
	}
}

func TestRunTellsTheOperatorWhatToFix(t *testing.T) {
	probe := healthyProbe()
	probe.txt = map[string][]string{}
	probe.ptr = map[string][]string{"203.0.113.10": {"other.example.net."}}
	probe.dialErr = map[string]error{"mail.example.com:25": errDial}
	probe.tlsErr = map[string]error{"mail.example.com:443": errTLS}
	probe.ip["10.113.0.203.zen.spamhaus.org"] = []net.IP{net.ParseIP("127.0.0.2")}

	lines := Run(context.Background(), []Record{
		{Name: "example.com", Type: "MX", Value: "10 mail.example.com"},
		{Name: "example.com", Type: "TXT", Value: "v=spf1 mx -all"},
	}, "mail.example.com", probe)
	text := join(lines)
	for _, want := range []string{
		`DNS example.com TXT: fix: publish "v=spf1 mx -all"`,
		"PTR 203.0.113.10: fix: set the reverse DNS to mail.example.com",
		"Port 25 mail.example.com: fix: allow inbound TCP from the internet",
		"TLS mail.example.com:443: fix: present a certificate that public clients trust",
		"Blocklist 203.0.113.10 zen.spamhaus.org: fix: this address is listed; request removal before sending mail",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if Healthy(lines) {
		t.Fatal("a broken host was reported healthy")
	}
}

func join(lines []Line) string {
	parts := make([]string, len(lines))
	for i, line := range lines {
		parts[i] = line.Text
	}
	return strings.Join(parts, "\n")
}

type errString string

func (e errString) Error() string { return string(e) }

const (
	errDial errString = "connection refused"
	errTLS  errString = "certificate signed by unknown authority"
)

func healthyProbe() *fakeProbe {
	return &fakeProbe{
		mx: map[string][]*net.MX{
			"example.com": {{Host: "mail.example.com.", Pref: 10}},
		},
		txt: map[string][]string{
			"example.com":                     {"v=spf1 mx -all"},
			"selector._domainkey.example.com": {"v=DKIM1; k=ed25519; p=abc"},
			"_dmarc.example.com":              {"v=DMARC1; p=reject"},
		},
		ip: map[string][]net.IP{
			"mail.example.com": {net.ParseIP("203.0.113.10")},
		},
		ptr: map[string][]string{
			"203.0.113.10": {"mail.example.com."},
		},
	}
}

type fakeProbe struct {
	mx      map[string][]*net.MX
	txt     map[string][]string
	ip      map[string][]net.IP
	ptr     map[string][]string
	dialErr map[string]error
	tlsErr  map[string]error
}

func (f *fakeProbe) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if records, ok := f.mx[name]; ok {
		return records, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (f *fakeProbe) LookupTXT(_ context.Context, name string) ([]string, error) {
	if records, ok := f.txt[name]; ok {
		return records, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (f *fakeProbe) LookupIP(_ context.Context, host string) ([]net.IP, error) {
	if records, ok := f.ip[host]; ok {
		return records, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func (f *fakeProbe) LookupAddr(_ context.Context, addr string) ([]string, error) {
	if names, ok := f.ptr[addr]; ok {
		return names, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: addr, IsNotFound: true}
}

func (f *fakeProbe) Dial(_ context.Context, address string) error {
	if f.dialErr == nil {
		return nil
	}
	return f.dialErr[address]
}

func (f *fakeProbe) TLS(_ context.Context, address, _ string) error {
	if f.tlsErr == nil {
		return nil
	}
	return f.tlsErr[address]
}
