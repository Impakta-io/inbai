// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

func accountWithQuota(name, domainID, roleID, secret string, quotaBytes int64) map[string]any {
	obj := account(name, domainID, roleID, secret)
	if quotaBytes > 0 {
		obj["quotas"] = map[string]any{"maxDiskQuota": quotaBytes}
	}
	return obj
}

// GetStorageQuota reads an account's disk quota and current usage from Stalwart.
func GetStorageQuota(ctx context.Context, client *Client, accountID string) (mailbackend.StorageQuota, error) {
	if client == nil {
		return mailbackend.StorageQuota{}, fmt.Errorf("missing stalwart client")
	}
	if accountID == "" {
		return mailbackend.StorageQuota{}, fmt.Errorf("quota needs an account id")
	}
	raw, err := client.invoke(ctx, "q", "x:Account/get", map[string]any{
		"ids":        []string{accountID},
		"properties": []string{"quotas", "usedDiskQuota"},
	})
	if err != nil {
		return mailbackend.StorageQuota{}, err
	}
	var got struct {
		List []struct {
			ID            string `json:"id"`
			UsedDiskQuota int64  `json:"usedDiskQuota"`
			Quotas        struct {
				MaxDiskQuota int64 `json:"maxDiskQuota"`
			} `json:"quotas"`
		} `json:"list"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return mailbackend.StorageQuota{}, err
	}
	if len(got.List) == 0 {
		return mailbackend.StorageQuota{}, fmt.Errorf("account %s not found", accountID)
	}
	return mailbackend.StorageQuota{
		LimitBytes: got.List[0].Quotas.MaxDiskQuota,
		UsedBytes:  got.List[0].UsedDiskQuota,
	}, nil
}

// SetStorageQuota sets or clears an account's maxDiskQuota in Stalwart.
// limitBytes zero clears the disk quota on the account.
func SetStorageQuota(ctx context.Context, client *Client, accountID string, limitBytes int64) error {
	if client == nil {
		return fmt.Errorf("missing stalwart client")
	}
	if accountID == "" {
		return fmt.Errorf("quota needs an account id")
	}
	if limitBytes < 0 {
		return fmt.Errorf("quota bytes must be zero or greater")
	}
	quotas := map[string]any{}
	if limitBytes > 0 {
		quotas["maxDiskQuota"] = limitBytes
	}
	raw, err := client.invoke(ctx, "q", "x:Account/set", map[string]any{
		"update": map[string]any{
			accountID: map[string]any{
				"quotas": quotas,
			},
		},
	})
	if err != nil {
		return err
	}
	return updatedID(raw, accountID)
}
