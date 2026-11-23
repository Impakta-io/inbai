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
	"strings"
)

const jmapPath = "/jmap/"

var jmapUsing = []string{
	"urn:ietf:params:jmap:core",
	"urn:ietf:params:jmap:mail",
	"urn:ietf:params:jmap:submission",
	"urn:stalwart:jmap",
}

// Client calls Stalwart's JMAP endpoint. It does not import Stalwart source.
type Client struct {
	BaseURL  string
	User     string
	Password string
	HTTP     *http.Client
	APIKey   string
}

type jmapCall struct {
	Name string
	Args any
	ID   string
}

func (c jmapCall) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{c.Name, c.Args, c.ID})
}

type jmapResponse struct {
	MethodResponses []jmapMethodResponse `json:"methodResponses"`
}

type jmapMethodResponse struct {
	Name string
	Raw  json.RawMessage
	ID   string
}

func (m *jmapMethodResponse) UnmarshalJSON(data []byte) error {
	var parts []json.RawMessage
	if err := json.Unmarshal(data, &parts); err != nil {
		return err
	}
	if len(parts) < 3 {
		return fmt.Errorf("stalwart returned a short method response")
	}
	if err := json.Unmarshal(parts[0], &m.Name); err != nil {
		return err
	}
	m.Raw = append(json.RawMessage(nil), parts[1]...)
	return json.Unmarshal(parts[2], &m.ID)
}

func (c *Client) invoke(ctx context.Context, id, name string, args any) (json.RawMessage, error) {
	payload, err := json.Marshal(map[string]any{
		"using":       jmapUsing,
		"methodCalls": []jmapCall{{Name: name, Args: args, ID: id}},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+jmapPath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stalwart %s returned HTTP %d", name, resp.StatusCode)
	}
	var parsed jmapResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("stalwart %s returned %w", name, err)
	}
	for _, method := range parsed.MethodResponses {
		if method.ID != id {
			continue
		}
		if method.Name == "error" {
			var problem struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			}
			_ = json.Unmarshal(method.Raw, &problem)
			if problem.Description != "" {
				return nil, fmt.Errorf("stalwart %s: %s: %s", name, problem.Type, problem.Description)
			}
			return nil, fmt.Errorf("stalwart %s: %s", name, problem.Type)
		}
		return method.Raw, nil
	}
	return nil, fmt.Errorf("stalwart %s: missing response %s", name, id)
}
