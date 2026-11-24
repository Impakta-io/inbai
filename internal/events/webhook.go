// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// ErrWebhookURL means a webhook target is not allowed.
var ErrWebhookURL = errors.New("webhook url is not allowed")

// LookupFunc resolves a host name. It exists so tests can supply addresses.
type LookupFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

func defaultLookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// Ranges that net/netip does not classify as private, loopback or link-local.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),  // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved, includes broadcast
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 embeds an IPv4 address
	netip.MustParsePrefix("2002::/16"),      // 6to4 embeds an IPv4 address
	netip.MustParsePrefix("100::/64"),       // discard prefix
	netip.MustParsePrefix("2001:db8::/32"),  // documentation
	netip.MustParsePrefix("fec0::/10"),      // deprecated site-local
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
}

// blockedAddr reports whether a webhook must never connect to ip.
func blockedAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func netipFromIP(ip net.IP) netip.Addr {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}
	}
	return addr
}

// CheckWebhookURL validates a webhook target when it is saved. Unless
// allowPrivate is set it accepts only https, and only a host whose every
// address is public. allowPrivate is an operator setting, never a caller's.
// A nil lookup uses the system resolver. The check at connect time in
// NewWebhookClient is the one that cannot be raced.
func CheckWebhookURL(ctx context.Context, raw string, allowPrivate bool, lookup LookupFunc) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil {
		return fmt.Errorf("%w: need an https url with a host and no credentials", ErrWebhookURL)
	}
	switch {
	case parsed.Scheme == "https":
	case parsed.Scheme == "http" && allowPrivate:
	default:
		return fmt.Errorf("%w: need an https url with a host and no credentials", ErrWebhookURL)
	}
	if allowPrivate {
		return nil
	}
	if lookup == nil {
		lookup = defaultLookup
	}
	if _, err := publicAddrs(ctx, parsed.Hostname(), lookup); err != nil {
		return err
	}
	return nil
}

// publicAddrs resolves host and fails unless every address is allowed.
func publicAddrs(ctx context.Context, host string, lookup LookupFunc) ([]netip.Addr, error) {
	denied := fmt.Errorf("%w: the host must resolve to a public address", ErrWebhookURL)
	if literal, err := netip.ParseAddr(host); err == nil {
		if blockedAddr(literal) {
			return nil, denied
		}
		return []netip.Addr{literal.Unmap()}, nil
	}
	found, err := lookup(ctx, host)
	if err != nil || len(found) == 0 {
		return nil, denied
	}
	out := make([]netip.Addr, 0, len(found))
	for _, a := range found {
		addr := netipFromIP(a.IP)
		if blockedAddr(addr) {
			return nil, denied
		}
		out = append(out, addr.Unmap())
	}
	return out, nil
}

// NewWebhookClient returns the client that delivers webhooks. Unless
// allowPrivate is set it refuses to connect to loopback, link-local, private,
// carrier-grade NAT, multicast, unspecified and other non-public addresses. It
// checks the address it is about to dial, so a later DNS change cannot get past
// the check made when the webhook was saved. It does not follow redirects, it
// ignores proxy variables, and a request times out after 10 seconds.
func NewWebhookClient(allowPrivate bool) *http.Client {
	return newWebhookClient(allowPrivate, nil)
}

func newWebhookClient(allowPrivate bool, lookup LookupFunc) *http.Client {
	if lookup == nil {
		lookup = defaultLookup
	}
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		// Control sees the resolved address of every connection attempt.
		Control: func(_, address string, _ syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("%w", ErrWebhookURL)
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || blockedAddr(ip) {
				return fmt.Errorf("%w: refused address", ErrWebhookURL)
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if allowPrivate {
				return dialer.DialContext(ctx, network, address)
			}
			addrs, err := publicAddrs(ctx, host, lookup)
			if err != nil {
				return nil, err
			}
			var last error
			for _, a := range addrs {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			return nil, last
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		// A redirect is returned as the response, so it counts as a failed delivery.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
