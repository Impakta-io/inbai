// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"fmt"
	"strings"
)

// accountBatch is how many accounts one Account/set request creates.
// Stalwart accepts up to a few hundred objects in one set.
var accountBatch = 200

// CreateInboxes creates many accounts. It sends one JMAP request per batch.
func CreateInboxes(ctx context.Context, client *Client, specs []InboxInput) ([]string, error) {
	if client == nil {
		return nil, fmt.Errorf("missing stalwart client")
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("no inboxes to create")
	}
	ids := make([]string, 0, len(specs))
	for start := 0; start < len(specs); start += accountBatch {
		end := start + accountBatch
		if end > len(specs) {
			end = len(specs)
		}
		made, err := createBatch(ctx, client, specs[start:end])
		if err != nil {
			return nil, err
		}
		ids = append(ids, made...)
	}
	return ids, nil
}

func createBatch(ctx context.Context, client *Client, specs []InboxInput) ([]string, error) {
	create := make(map[string]any, len(specs))
	keys := make([]string, len(specs))
	for i, spec := range specs {
		local := strings.ToLower(strings.TrimSpace(spec.Local))
		if local == "" || strings.ContainsAny(local, " @/\t") {
			return nil, fmt.Errorf("local part %q is not an address", spec.Local)
		}
		if spec.DomainID == "" || spec.RoleID == "" {
			return nil, fmt.Errorf("inbox needs a domain and a role")
		}
		secret, err := randomSecret()
		if err != nil {
			return nil, err
		}
		keys[i] = fmt.Sprintf("u%d", i)
		create[keys[i]] = accountWithQuota(local, spec.DomainID, spec.RoleID, secret, spec.QuotaBytes)
	}
	raw, err := client.invoke(ctx, "b", "x:Account/set", map[string]any{"create": create})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(keys))
	for i, key := range keys {
		ids[i], err = createdID(raw, key)
		if err != nil {
			return nil, err
		}
	}
	return ids, nil
}
