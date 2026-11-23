// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ListInboxes returns every account, paging the query until it is exhausted.
func ListInboxes(ctx context.Context, client *Client) ([]mailAccount, error) {
	if client == nil {
		return nil, fmt.Errorf("missing stalwart client")
	}
	var out []mailAccount
	for position := 0; ; position += 200 {
		raw, err := client.invoke(ctx, "q", "x:Account/query", map[string]any{
			"position": position,
			"limit":    200,
		})
		if err != nil {
			return nil, err
		}
		var query struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(raw, &query); err != nil {
			return nil, err
		}
		if len(query.IDs) == 0 {
			return out, nil
		}
		raw, err = client.invoke(ctx, "g", "x:Account/get", map[string]any{
			"ids":        query.IDs,
			"properties": []string{"name"},
		})
		if err != nil {
			return nil, err
		}
		var got struct {
			List []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"list"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			return nil, err
		}
		for _, item := range got.List {
			out = append(out, mailAccount{ID: item.ID, Local: item.Name})
		}
		if len(query.IDs) < 200 {
			return out, nil
		}
	}
}

type mailAccount struct {
	ID    string
	Local string
}

// DeleteInbox removes one account.
func DeleteInbox(ctx context.Context, client *Client, id string) error {
	if client == nil {
		return fmt.Errorf("missing stalwart client")
	}
	if id == "" {
		return fmt.Errorf("inbox id is required")
	}
	raw, err := client.invoke(ctx, "d", "x:Account/set", map[string]any{
		"destroy": []string{id},
	})
	if err != nil {
		return err
	}
	var result struct {
		NotDestroyed map[string]json.RawMessage `json:"notDestroyed"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if len(result.NotDestroyed) > 0 {
		return fmt.Errorf("account %s was not deleted", id)
	}
	return nil
}

func (c *Client) download(ctx context.Context, accountID, blobID string) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("missing stalwart client")
	}
	if accountID == "" || blobID == "" {
		return nil, fmt.Errorf("download needs an account and a blob")
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/jmap/download/" + url.PathEscape(accountID) + "/" + url.PathEscape(blobID) + "/file"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	switch {
	case c.APIKey != "":
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	default:
		req.SetBasicAuth(c.User, c.Password)
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 25<<20+1))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %d", res.StatusCode)
	}
	if len(raw) > 25<<20 {
		return nil, fmt.Errorf("attachment is larger than 25MB")
	}
	return raw, nil
}
