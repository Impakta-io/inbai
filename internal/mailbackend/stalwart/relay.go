// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const relayRouteName = "inbai-relay"

// Relay is an authenticated SMTP server that receives mail for other domains.
type Relay struct {
	Host              string
	Port              int
	User              string
	Password          string
	ImplicitTLS       bool
	AllowInvalidCerts bool
}

// SetRelay sends mail for other domains through one authenticated SMTP relay.
// Mail for a domain this server hosts stays local.
func SetRelay(ctx context.Context, client *Client, relay Relay) error {
	if client == nil {
		return fmt.Errorf("missing stalwart client")
	}
	relay.Host = strings.TrimSpace(relay.Host)
	relay.User = strings.TrimSpace(relay.User)
	if relay.Host == "" || strings.ContainsAny(relay.Host, " /\t") || strings.Contains(relay.Host, "://") {
		return fmt.Errorf("relay host %q is not a host name", relay.Host)
	}
	if relay.Port == 0 {
		relay.Port = 587
	}
	if relay.Port < 1 || relay.Port > 65535 {
		return fmt.Errorf("relay port %d is outside 1-65535", relay.Port)
	}
	if relay.User == "" || relay.Password == "" {
		return fmt.Errorf("relay user and password are required")
	}
	route := map[string]any{
		"@type":             "Relay",
		"name":              relayRouteName,
		"host":              relay.Host,
		"port":              relay.Port,
		"protocol":          "smtp",
		"implicitTls":       relay.ImplicitTLS,
		"allowInvalidCerts": relay.AllowInvalidCerts,
		"authUsername":      relay.User,
		"authSecret":        map[string]any{"@type": "Value", "secret": relay.Password},
	}
	return applyRoute(ctx, client, route, relayRouteName)
}

// DirectDelivery sends mail for other domains to their MX hosts.
func DirectDelivery(ctx context.Context, client *Client) error {
	if client == nil {
		return fmt.Errorf("missing stalwart client")
	}
	return applyRoute(ctx, client, nil, "mx")
}

func applyRoute(ctx context.Context, client *Client, route map[string]any, destination string) error {
	id, err := relayRouteID(ctx, client)
	if err != nil {
		return err
	}
	calls := []jmapCall{{
		Name: "x:MtaOutboundStrategy/set",
		Args: map[string]any{"update": map[string]any{"singleton": map[string]any{"route": routeExpression(destination)}}},
		ID:   "o",
	}, {
		Name: "x:Action/ReloadSettings/set",
		Args: map[string]any{"create": map[string]any{"a": map[string]any{}}},
		ID:   "z",
	}}
	switch {
	case route == nil && id != "":
		calls = append([]jmapCall{{Name: "x:MtaRoute/set", Args: map[string]any{"destroy": []string{id}}, ID: "r"}}, calls...)
	case route != nil && id == "":
		calls = append([]jmapCall{{Name: "x:MtaRoute/set", Args: map[string]any{"create": map[string]any{"sm": route}}, ID: "r"}}, calls...)
	case route != nil:
		calls = append([]jmapCall{{Name: "x:MtaRoute/set", Args: map[string]any{"update": map[string]any{id: route}}, ID: "r"}}, calls...)
	}
	found, err := client.invokeMany(ctx, calls)
	if err != nil {
		return err
	}
	if route != nil && id == "" {
		if _, err := createdID(found["r"], "sm"); err != nil {
			return err
		}
	}
	if route == nil && id != "" {
		if err := destroyedID(found["r"], id); err != nil {
			return err
		}
	}
	if route != nil && id != "" {
		if err := updatedID(found["r"], id); err != nil {
			return err
		}
	}
	return updatedID(found["o"], "singleton")
}

func routeExpression(destination string) map[string]any {
	return map[string]any{
		"match": map[string]any{
			"0": map[string]any{"if": "is_local_domain(rcpt_domain)", "then": "'local'"},
		},
		"else": "'" + destination + "'",
	}
}

func relayRouteID(ctx context.Context, client *Client) (string, error) {
	found, err := client.invokeMany(ctx, []jmapCall{
		{Name: "x:MtaRoute/query", Args: map[string]any{}, ID: "q"},
		{Name: "x:MtaRoute/get", Args: map[string]any{
			"properties": []string{"id", "name"},
			"#ids":       map[string]any{"resultOf": "q", "name": "x:MtaRoute/query", "path": "/ids"},
		}, ID: "g"},
	})
	if err != nil {
		return "", err
	}
	var list struct {
		List []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"list"`
	}
	if err := json.Unmarshal(found["g"], &list); err != nil {
		return "", err
	}
	for _, route := range list.List {
		if route.Name == relayRouteName {
			return route.ID, nil
		}
	}
	return "", nil
}

func destroyedID(raw json.RawMessage, id string) error {
	var body struct {
		Destroyed    []string `json:"destroyed"`
		NotDestroyed map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"notDestroyed"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	if missing, ok := body.NotDestroyed[id]; ok {
		detail := missing.Type
		if missing.Description != "" {
			detail = missing.Type + ": " + missing.Description
		}
		return fmt.Errorf("stalwart %s: %s", id, detail)
	}
	for _, destroyed := range body.Destroyed {
		if destroyed == id {
			return nil
		}
	}
	return fmt.Errorf("stalwart %s: missing destroy", id)
}
