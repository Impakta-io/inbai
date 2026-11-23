// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"crypto/tls"
	"net"
	"time"
)

// Live uses the system resolver and dials the mail host directly.
func Live() Probe { return liveProbe{} }

type liveProbe struct{}

func (liveProbe) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return net.DefaultResolver.LookupMX(ctx, name)
}

func (liveProbe) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

func (liveProbe) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func (liveProbe) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	return net.DefaultResolver.LookupAddr(ctx, addr)
}

func (liveProbe) Dial(ctx context.Context, address string) error {
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func (liveProbe) TLS(ctx context.Context, address, serverName string) error {
	conn, err := (&tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		Config:    &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12},
	}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}
