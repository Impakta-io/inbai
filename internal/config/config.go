// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package config

// DefaultRetentionDays is the retention period inbai init writes.
// Zero keeps mail. The server deletes a message once it is older than the period.
const DefaultRetentionDays = 30

// DefaultAPIURL is where inbaid serve listens unless --addr is set.
const DefaultAPIURL = "http://127.0.0.1:8787"

// File is the operator config inbai init writes.
// It holds the service API key, so the file mode is 0600.
type File struct {
	Domain            string            `json:"domain"`
	DomainID          string            `json:"domainId,omitempty"`
	Hostname          string            `json:"hostname"`
	StalwartURL       string            `json:"stalwartUrl"`
	ServiceAccountID  string            `json:"serviceAccountId"`
	OperatorAccountID string            `json:"operatorAccountId"`
	InboxRoleID       string            `json:"inboxRoleId"`
	ServiceRoleID     string            `json:"serviceRoleId"`
	APIKey            string            `json:"apiKey"`
	APIURL            string            `json:"apiUrl,omitempty"`
	ClientID          string            `json:"clientId,omitempty"`
	ClientSecret      string            `json:"clientSecret,omitempty"`
	OperatorToken     string            `json:"operatorToken,omitempty"`
	RetentionDays     int               `json:"retentionDays"`
	Inboxes           map[string]string `json:"inboxes,omitempty"`
}
