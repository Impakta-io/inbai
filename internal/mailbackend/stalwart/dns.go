// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrDomainNotConfigured means Stalwart has no domain with the requested name.
var ErrDomainNotConfigured = errors.New("domain is not configured in Stalwart")

func checkDomainName(client *Client, domain string) error {
	if client == nil {
		return fmt.Errorf("missing stalwart client")
	}
	if strings.TrimSpace(domain) == "" || !strings.Contains(domain, ".") || strings.ContainsAny(domain, " /\t") {
		return fmt.Errorf("domain %q is not a DNS name", domain)
	}
	return nil
}

// CreateDomain registers the domain in Stalwart. Only a command whose job is
// to add the domain may call it. It does not check whether the domain exists.
func CreateDomain(ctx context.Context, client *Client, domain string) error {
	if err := checkDomainName(client, domain); err != nil {
		return err
	}
	_, err := ensureDomain(ctx, client, domain)
	return err
}

// ZoneFile returns the BIND zone Stalwart computed for the domain.
// It only reads. It returns ErrDomainNotConfigured when the server has no
// such domain.
func ZoneFile(ctx context.Context, client *Client, domain string) (string, error) {
	if err := checkDomainName(client, domain); err != nil {
		return "", err
	}
	raw, err := client.invoke(ctx, "q", "x:Domain/query", map[string]any{
		"filter": map[string]any{"name": domain},
	})
	if err != nil {
		return "", err
	}
	var query struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(raw, &query); err != nil {
		return "", err
	}
	if len(query.IDs) == 0 {
		return "", fmt.Errorf("%w: %s", ErrDomainNotConfigured, domain)
	}
	id, err := domainID(ctx, client, query.IDs, domain)
	if err != nil {
		return "", err
	}
	raw, err = client.invoke(ctx, "g", "x:Domain/get", map[string]any{
		"ids":        []string{id},
		"properties": []string{"name", "dnsZoneFile"},
	})
	if err != nil {
		return "", err
	}
	var got struct {
		List []struct {
			Name        string `json:"name"`
			DNSZoneFile string `json:"dnsZoneFile"`
		} `json:"list"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return "", err
	}
	for _, item := range got.List {
		if !strings.EqualFold(item.Name, domain) && item.Name != "" {
			continue
		}
		if strings.TrimSpace(item.DNSZoneFile) == "" {
			return "", fmt.Errorf("stalwart domain %s has no zone file", domain)
		}
		return item.DNSZoneFile, nil
	}
	return "", fmt.Errorf("stalwart domain %s has no zone file", domain)
}

// LookupDomainID returns the id of a domain that already exists.
func LookupDomainID(ctx context.Context, client *Client, domain string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("missing stalwart client")
	}
	raw, err := client.invoke(ctx, "q", "x:Domain/query", map[string]any{
		"filter": map[string]any{"name": domain},
	})
	if err != nil {
		return "", err
	}
	var query struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(raw, &query); err != nil {
		return "", err
	}
	if len(query.IDs) == 0 {
		return "", fmt.Errorf("stalwart has no domain named %s", domain)
	}
	return domainID(ctx, client, query.IDs, domain)
}

func domainID(ctx context.Context, client *Client, ids []string, domain string) (string, error) {
	raw, err := client.invoke(ctx, "g", "x:Domain/get", map[string]any{
		"ids":        ids,
		"properties": []string{"name"},
	})
	if err != nil {
		return "", err
	}
	var got struct {
		List []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"list"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return "", err
	}
	for _, item := range got.List {
		if strings.EqualFold(item.Name, domain) {
			return item.ID, nil
		}
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return "", fmt.Errorf("stalwart has no domain named %s", domain)
}
