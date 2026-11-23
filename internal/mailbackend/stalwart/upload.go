// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c *Client) upload(ctx context.Context, accountID, contentType string, data []byte) (string, error) {
	if c == nil {
		return "", fmt.Errorf("missing stalwart client")
	}
	if accountID == "" {
		return "", fmt.Errorf("upload needs an account")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/jmap/upload/" + url.PathEscape(accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", contentType)
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
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("stalwart upload returned HTTP %d", resp.StatusCode)
	}
	var got struct {
		BlobID string `json:"blobId"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		return "", err
	}
	if got.BlobID == "" {
		return "", fmt.Errorf("stalwart upload returned no blob id")
	}
	return got.BlobID, nil
}
