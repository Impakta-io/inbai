// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	inboxRoleDescription    = "inbAI inbox"
	serviceRoleDescription  = "inbAI service"
	operatorRoleDescription = "inbAI operator"
	recoveryUser            = "admin"
)

// Input is the operator's choice for the first boot.
// ResetOperatorPassword sets OperatorPassword on an operator that already exists.
// Without it, a rerun leaves that operator's password alone.
type Input struct {
	Domain                string
	Hostname              string
	OperatorPassword      string
	ResetOperatorPassword bool
	CreateAPIKey          bool
}

// ErrAPIKeyRejected means Stalwart answered and refused the API key.
var ErrAPIKeyRejected = errors.New("stalwart rejected the API key")

// Result is what the first boot created or found.
type Result struct {
	DomainID          string
	Hostname          string
	InboxRoleID       string
	ServiceRoleID     string
	ServiceAccountID  string
	OperatorAccountID string
	APIKey            string
}

var inboxPermissions = []string{
	"authenticate",
	"emailReceive",
	"emailSend",
	"jmapMailboxGet",
	"jmapMailboxChanges",
	"jmapMailboxQuery",
	"jmapMailboxQueryChanges",
	"jmapThreadGet",
	"jmapThreadChanges",
	"jmapEmailGet",
	"jmapEmailChanges",
	"jmapEmailQuery",
	"jmapEmailQueryChanges",
	"jmapEmailCreate",
	"jmapEmailSubmissionGet",
	"jmapEmailSubmissionChanges",
	"jmapEmailSubmissionCreate",
	"jmapIdentityGet",
	"jmapIdentityChanges",
}

var servicePermissions = append(append([]string{}, inboxPermissions...),
	"impersonate",
	"sysAccountGet",
	"sysAccountCreate",
	"sysAccountUpdate",
	"sysAccountQuery",
	"sysApiKeyCreate",
	"sysDomainGet",
	"sysDomainCreate",
	"sysDomainQuery",
	"sysRoleGet",
	"sysRoleQuery",
)

// The operator can change where outbound mail goes. The service account cannot.
var operatorPermissions = append(append([]string{}, servicePermissions...),
	"sysMtaRouteGet",
	"sysMtaRouteQuery",
	"sysMtaRouteCreate",
	"sysMtaRouteUpdate",
	"sysMtaRouteDestroy",
	"sysMtaOutboundStrategyGet",
	"sysMtaOutboundStrategyUpdate",
)

// Bootstrap creates the domain, the inbox and service roles, the operator,
// and the service account. It is safe to run again.
func Bootstrap(ctx context.Context, client *Client, in Input) (Result, error) {
	if client == nil {
		return Result{}, fmt.Errorf("missing stalwart client")
	}
	if strings.TrimSpace(in.Domain) == "" || !strings.Contains(in.Domain, ".") || strings.ContainsAny(in.Domain, " /\t") {
		return Result{}, fmt.Errorf("domain %q is not a DNS name", in.Domain)
	}
	if in.OperatorPassword == "" {
		return Result{}, fmt.Errorf("operator password is empty")
	}
	hostname := in.Hostname
	if hostname == "" {
		hostname = "mail." + in.Domain
	}
	if client.User == "" {
		client.User = recoveryUser
	}

	domainID, err := ensureDomain(ctx, client, in.Domain)
	if err != nil {
		return Result{}, err
	}
	if _, err := client.invoke(ctx, "s", "x:SystemSettings/set", map[string]any{
		"update": map[string]any{
			"singleton": map[string]any{
				"defaultHostname": hostname,
				"defaultDomainId": domainID,
			},
		},
	}); err != nil {
		return Result{}, err
	}
	inboxRole, err := ensureRole(ctx, client, "inbox", inboxRoleDescription, permSet(inboxPermissions))
	if err != nil {
		return Result{}, err
	}
	serviceRole, err := ensureRole(ctx, client, "svc", serviceRoleDescription, permSet(servicePermissions))
	if err != nil {
		return Result{}, err
	}
	operatorRole, err := ensureRole(ctx, client, "op", operatorRoleDescription, permSet(operatorPermissions))
	if err != nil {
		return Result{}, err
	}
	serviceSecret, err := randomSecret()
	if err != nil {
		return Result{}, err
	}
	operatorID, serviceID, err := ensureAccounts(ctx, client, domainID, operatorRole, serviceRole, in.OperatorPassword, serviceSecret, in.ResetOperatorPassword)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		DomainID:          domainID,
		Hostname:          hostname,
		InboxRoleID:       inboxRole,
		ServiceRoleID:     serviceRole,
		OperatorAccountID: operatorID,
		ServiceAccountID:  serviceID,
	}
	if !in.CreateAPIKey {
		return result, nil
	}
	secret, err := CreateAPIKey(ctx, client, serviceID)
	if err != nil {
		return Result{}, err
	}
	result.APIKey = secret
	return result, nil
}

// CreateAPIKey issues a new API key for the service account and returns its secret.
// Stalwart shows the secret once. The client must be the recovery administrator.
func CreateAPIKey(ctx context.Context, client *Client, serviceAccountID string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("missing stalwart client")
	}
	if client.User == "" {
		client.User = recoveryUser
	}
	raw, err := client.invoke(ctx, "k", "x:ApiKey/set", map[string]any{
		"accountId": serviceAccountID,
		"create": map[string]any{
			"k1": map[string]any{
				"description": "inbAI",
				"permissions": map[string]any{"@type": "Inherit"},
				"allowedIps":  map[string]any{},
			},
		},
	})
	if err != nil {
		return "", err
	}
	return createdSecret(raw, "k1")
}

// CheckAPIKey makes one authenticated JMAP call with client.APIKey.
// It returns ErrAPIKeyRejected when Stalwart answers 401 or 403.
// Any other failure, such as a network error or a 5xx, is returned as is:
// the key was not checked, so the caller must not treat it as bad.
func CheckAPIKey(ctx context.Context, client *Client) error {
	if client == nil || client.APIKey == "" {
		return fmt.Errorf("missing stalwart API key")
	}
	payload, err := json.Marshal(map[string]any{
		"using":       []string{"urn:ietf:params:jmap:core"},
		"methodCalls": []jmapCall{{Name: "Core/echo", Args: map[string]any{}, ID: "e"}},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(client.BaseURL, "/")+jmapPath, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+client.APIKey)
	httpClient := client.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w (HTTP %d)", ErrAPIKeyRejected, resp.StatusCode)
	default:
		return fmt.Errorf("stalwart Core/echo returned HTTP %d", resp.StatusCode)
	}
}

func ensureDomain(ctx context.Context, client *Client, name string) (string, error) {
	raw, err := client.invoke(ctx, "d", "x:Domain/set", map[string]any{
		"create": map[string]any{
			"d1": map[string]any{
				"name":                  name,
				"certificateManagement": map[string]any{"@type": "Manual"},
				"dkimManagement":        map[string]any{"@type": "Automatic"},
				"dnsManagement":         map[string]any{"@type": "Manual"},
				"subAddressing":         map[string]any{"@type": "Enabled"},
			},
		},
	})
	if err != nil {
		return "", err
	}
	return createdID(raw, "d1")
}

func ensureRole(ctx context.Context, client *Client, createKey, description string, perms map[string]bool) (string, error) {
	raw, err := client.invoke(ctx, "q", "x:Role/query", map[string]any{})
	if err != nil {
		return "", err
	}
	var query struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(raw, &query); err != nil {
		return "", err
	}
	if len(query.IDs) > 0 {
		got, err := client.invoke(ctx, "g", "x:Role/get", map[string]any{"ids": query.IDs})
		if err != nil {
			return "", err
		}
		var list struct {
			List []struct {
				ID          string `json:"id"`
				Description string `json:"description"`
			} `json:"list"`
		}
		if err := json.Unmarshal(got, &list); err != nil {
			return "", err
		}
		for _, role := range list.List {
			if role.Description == description {
				return role.ID, nil
			}
		}
	}
	raw, err = client.invoke(ctx, "r", "x:Role/set", map[string]any{
		"create": map[string]any{
			createKey: map[string]any{
				"description":         description,
				"enabledPermissions":  perms,
				"disabledPermissions": map[string]any{},
				"roleIds":             map[string]any{},
			},
		},
	})
	if err != nil {
		return "", err
	}
	return createdID(raw, createKey)
}

func ensureAccounts(ctx context.Context, client *Client, domainID, operatorRole, serviceRole, operatorPassword, servicePassword string, resetOperatorPassword bool) (string, string, error) {
	raw, err := client.invoke(ctx, "a", "x:Account/set", map[string]any{
		"create": map[string]any{
			"operator": account("operator", domainID, operatorRole, operatorPassword),
			"svc":      account("service", domainID, serviceRole, servicePassword),
		},
	})
	if err != nil {
		return "", "", err
	}
	operatorID, err := createdID(raw, "operator")
	if err != nil {
		return "", "", err
	}
	serviceID, err := createdID(raw, "svc")
	if err != nil {
		return "", "", err
	}
	var body setResult
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", "", err
	}
	if _, existed := body.NotCreated["operator"]; existed {
		patch := map[string]any{
			"roles": map[string]any{
				"@type":   "Custom",
				"roleIds": map[string]bool{operatorRole: true},
			},
		}
		if resetOperatorPassword {
			patch["credentials"] = passwordCredentials(operatorPassword)
		}
		updated, err := client.invoke(ctx, "u", "x:Account/set", map[string]any{
			"update": map[string]any{operatorID: patch},
		})
		if err != nil {
			return "", "", err
		}
		if err := updatedID(updated, operatorID); err != nil {
			return "", "", err
		}
	}
	return operatorID, serviceID, nil
}

func account(name, domainID, roleID, secret string) map[string]any {
	return map[string]any{
		"@type":       "User",
		"name":        name,
		"domainId":    domainID,
		"credentials": passwordCredentials(secret),
		"roles": map[string]any{
			"@type":   "Custom",
			"roleIds": map[string]bool{roleID: true},
		},
		"permissions":      map[string]any{"@type": "Inherit"},
		"encryptionAtRest": map[string]any{"@type": "Disabled"},
	}
}

// passwordCredentials is an account's credential list with one password.
// On an update it replaces the account's credentials.
func passwordCredentials(secret string) map[string]any {
	return map[string]any{
		"0": map[string]any{"@type": "Password", "secret": secret},
	}
}

func permSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

type setResult struct {
	Created map[string]struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	} `json:"created"`
	NotCreated map[string]struct {
		Type        string `json:"type"`
		Description string `json:"description"`
		ObjectID    struct {
			ID string `json:"id"`
		} `json:"objectId"`
	} `json:"notCreated"`
}

func createdID(raw json.RawMessage, key string) (string, error) {
	item, err := lookupSetItem(raw, key)
	if err != nil {
		return "", err
	}
	if item.ID != "" {
		return item.ID, nil
	}
	return "", fmt.Errorf("stalwart %s: missing id", key)
}

func updatedID(raw json.RawMessage, id string) error {
	var body struct {
		Updated    map[string]json.RawMessage `json:"updated"`
		NotUpdated map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"notUpdated"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	if missing, ok := body.NotUpdated[id]; ok {
		detail := missing.Type
		if missing.Description != "" {
			detail = missing.Type + ": " + missing.Description
		}
		return fmt.Errorf("stalwart %s: %s", id, detail)
	}
	if _, ok := body.Updated[id]; !ok {
		return fmt.Errorf("stalwart %s: missing update", id)
	}
	return nil
}

func createdSecret(raw json.RawMessage, key string) (string, error) {
	item, err := lookupSetItem(raw, key)
	if err != nil {
		return "", err
	}
	if item.Secret == "" {
		return "", fmt.Errorf("stalwart %s: missing secret", key)
	}
	return item.Secret, nil
}

type setItem struct {
	ID     string
	Secret string
}

func lookupSetItem(raw json.RawMessage, key string) (setItem, error) {
	var body setResult
	if err := json.Unmarshal(raw, &body); err != nil {
		return setItem{}, err
	}
	if created, ok := body.Created[key]; ok {
		return setItem{ID: created.ID, Secret: created.Secret}, nil
	}
	missing, ok := body.NotCreated[key]
	if !ok {
		return setItem{}, fmt.Errorf("stalwart %s: missing result", key)
	}
	if missing.ObjectID.ID != "" {
		return setItem{ID: missing.ObjectID.ID}, nil
	}
	detail := missing.Type
	if missing.Description != "" {
		detail = missing.Type + ": " + missing.Description
	}
	return setItem{}, fmt.Errorf("stalwart %s: %s", key, detail)
}

func randomSecret() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
