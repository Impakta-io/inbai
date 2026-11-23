// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Impakta-io/inbai/internal/service"
)

func applySendLimits(svc *service.Service, getenv func(string) string) error {
	if getenv == nil {
		return nil
	}
	inbox, err := parseLimit(getenv, "INBAI_QUOTA_INBOX_DAILY", service.DefaultInboxDaily)
	if err != nil {
		return err
	}
	domain, err := parseLimit(getenv, "INBAI_QUOTA_DOMAIN_DAILY", service.DefaultDomainDaily)
	if err != nil {
		return err
	}
	workspace, err := parseLimit(getenv, "INBAI_QUOTA_WORKSPACE_DAILY", service.DefaultWorkspaceDaily)
	if err != nil {
		return err
	}
	rcpt, err := parseLimit(getenv, "INBAI_RATE_RECIPIENT_PER_MINUTE", service.DefaultRecipientPerMinute)
	if err != nil {
		return err
	}
	svc.SetSendLimits(inbox, domain, workspace, rcpt)
	return nil
}

func parseLimit(getenv func(string) string, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return n, nil
}
