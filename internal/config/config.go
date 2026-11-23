// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package config

// DefaultRetentionDays is the retention period inbai init reports.
// An operator can change or disable retention per server or per inbox.
const DefaultRetentionDays = 30

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
	RetentionDays     int               `json:"retentionDays"`
	Inboxes           map[string]string `json:"inboxes,omitempty"`
}
