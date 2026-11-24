// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Record is one MX or TXT entry from the domain's zone file.
type Record struct {
	Name  string
	Type  string
	Value string
}

// Line is one check the operator can read.
type Line struct {
	OK   bool
	Text string
}

// Probe is the network access doctor uses. Tests supply a fake.
type Probe interface {
	LookupMX(context.Context, string) ([]*net.MX, error)
	LookupTXT(context.Context, string) ([]string, error)
	LookupIP(context.Context, string) ([]net.IP, error)
	LookupAddr(context.Context, string) ([]string, error)
	Dial(context.Context, string) error
	TLS(context.Context, string, string) error
}

var blocklists = []string{"zen.spamhaus.org", "bl.spamcop.net"}

// Run checks the zone records and the mail hosts they name.
// fallbackHost is checked when the zone has no MX record.
func Run(ctx context.Context, records []Record, fallbackHost string, probe Probe) []Line {
	var lines []Line
	lines = append(lines, dnsLines(ctx, records, probe)...)
	hosts := mxHosts(records)
	if len(hosts) == 0 && fallbackHost != "" {
		hosts = []string{fallbackHost}
	}
	for _, host := range hosts {
		lines = append(lines, hostLines(ctx, host, probe)...)
	}
	return lines
}

// Healthy reports whether every check passed.
func Healthy(lines []Line) bool {
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		if !line.OK {
			return false
		}
	}
	return true
}

func dnsLines(ctx context.Context, records []Record, probe Probe) []Line {
	var lines []Line
	mxCache := map[string]mxAnswer{}
	txtCache := map[string]txtAnswer{}
	for _, record := range records {
		if record.Type != "MX" && record.Type != "TXT" {
			continue
		}
		var status string
		var ok bool
		switch record.Type {
		case "MX":
			answer, seen := mxCache[record.Name]
			if !seen {
				hosts, err := probe.LookupMX(ctx, record.Name)
				answer = mxAnswer{hosts: hosts, err: err}
				mxCache[record.Name] = answer
			}
			ok, status = matchMX(record.Value, answer)
		default:
			answer, seen := txtCache[record.Name]
			if !seen {
				values, err := probe.LookupTXT(ctx, record.Name)
				answer = txtAnswer{values: values, err: err}
				txtCache[record.Name] = answer
			}
			ok, status = matchTXT(record.Value, answer)
		}
		lines = append(lines, Line{OK: ok, Text: fmt.Sprintf("DNS %s %s: %s", record.Name, record.Type, status)})
	}
	return lines
}

type mxAnswer struct {
	hosts []*net.MX
	err   error
}

type txtAnswer struct {
	values []string
	err    error
}

func matchMX(want string, answer mxAnswer) (bool, string) {
	if notFound(answer.err) || (answer.err == nil && len(answer.hosts) == 0) {
		return false, fmt.Sprintf("fix: publish %q", want)
	}
	if answer.err != nil {
		return false, "fix: the lookup failed"
	}
	for _, host := range answer.hosts {
		got := fmt.Sprintf("%d %s", host.Pref, normalize(host.Host))
		if got == want {
			return true, "ok"
		}
	}
	return false, fmt.Sprintf("fix: the published record does not match %q", want)
}

func matchTXT(want string, answer txtAnswer) (bool, string) {
	if notFound(answer.err) || (answer.err == nil && len(answer.values) == 0) {
		return false, fmt.Sprintf("fix: publish %q", want)
	}
	if answer.err != nil {
		return false, "fix: the lookup failed"
	}
	for _, value := range answer.values {
		if value == want {
			return true, "ok"
		}
	}
	return false, fmt.Sprintf("fix: the published record does not match %q", want)
}

func hostLines(ctx context.Context, host string, probe Probe) []Line {
	var lines []Line
	ips, err := probe.LookupIP(ctx, host)
	if notFound(err) || (err == nil && len(ips) == 0) {
		lines = append(lines, Line{Text: fmt.Sprintf("Address %s: fix: publish an A or AAAA record", host)})
	} else if err != nil {
		lines = append(lines, Line{Text: fmt.Sprintf("Address %s: fix: the lookup failed", host)})
	} else {
		for _, ip := range ips {
			lines = append(lines, ptrLine(ctx, host, ip, probe))
			lines = append(lines, blocklistLines(ctx, ip, probe)...)
		}
	}
	port := net.JoinHostPort(host, "25")
	if err := probe.Dial(ctx, port); err != nil {
		lines = append(lines, Line{Text: "Port 25 " + host + ": fix: allow inbound TCP from the internet"})
	} else {
		lines = append(lines, Line{OK: true, Text: "Port 25 " + host + ": ok"})
	}
	tlsAddr := net.JoinHostPort(host, "443")
	if err := probe.TLS(ctx, tlsAddr, host); err != nil {
		lines = append(lines, Line{Text: "TLS " + tlsAddr + ": fix: present a certificate that public clients trust"})
	} else {
		lines = append(lines, Line{OK: true, Text: "TLS " + tlsAddr + ": ok"})
	}
	return lines
}

func ptrLine(ctx context.Context, host string, ip net.IP, probe Probe) Line {
	addr := ip.String()
	names, err := probe.LookupAddr(ctx, addr)
	text := fmt.Sprintf("PTR %s: fix: set the reverse DNS to %s", addr, host)
	if err != nil && !notFound(err) {
		return Line{Text: fmt.Sprintf("PTR %s: fix: the lookup failed", addr)}
	}
	for _, name := range names {
		if normalize(name) == host {
			return Line{OK: true, Text: fmt.Sprintf("PTR %s: ok", addr)}
		}
	}
	return Line{Text: text}
}

func blocklistLines(ctx context.Context, ip net.IP, probe Probe) []Line {
	reversed, ok := reverseIPv4(ip)
	if !ok {
		return []Line{{OK: true, Text: fmt.Sprintf("Blocklist %s: not checked (the lists are queried for IPv4 addresses only)", ip)}}
	}
	var lines []Line
	for _, list := range blocklists {
		name := reversed + "." + list
		found, err := probe.LookupIP(ctx, name)
		addr := ip.String()
		switch {
		case notFound(err) || (err == nil && len(found) == 0):
			lines = append(lines, Line{OK: true, Text: fmt.Sprintf("Blocklist %s %s: ok", addr, list)})
		case err != nil:
			lines = append(lines, Line{Text: fmt.Sprintf("Blocklist %s %s: fix: the lookup failed", addr, list)})
		case refused(found):
			lines = append(lines, Line{Text: fmt.Sprintf("Blocklist %s %s: fix: the query was refused; check this list from a public resolver", addr, list)})
		case listed(found):
			lines = append(lines, Line{Text: fmt.Sprintf("Blocklist %s %s: fix: this address is listed; request removal before sending mail", addr, list)})
		default:
			lines = append(lines, Line{OK: true, Text: fmt.Sprintf("Blocklist %s %s: ok", addr, list)})
		}
	}
	return lines
}

func mxHosts(records []Record) []string {
	var hosts []string
	seen := map[string]bool{}
	for _, record := range records {
		if record.Type != "MX" {
			continue
		}
		fields := strings.Fields(record.Value)
		if len(fields) < 2 {
			continue
		}
		host := normalize(fields[len(fields)-1])
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	return hosts
}

func reverseIPv4(ip net.IP) (string, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return "", false
	}
	return fmt.Sprintf("%d.%d.%d.%d", v4[3], v4[2], v4[1], v4[0]), true
}

func listed(ips []net.IP) bool {
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil || v4[0] != 127 {
			continue
		}
		if v4[1] == 255 && v4[2] == 255 {
			continue
		}
		if v4[1] != 0 || v4[2] != 0 || v4[3] >= 2 {
			return true
		}
	}
	return false
}

func refused(ips []net.IP) bool {
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 != nil && v4[0] == 127 && v4[1] == 255 && v4[2] == 255 {
			return true
		}
	}
	return false
}

func notFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

func normalize(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}
