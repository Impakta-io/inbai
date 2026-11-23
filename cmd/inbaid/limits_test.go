// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

func TestApplySendLimitsFromEnv(t *testing.T) {
	svc := service.New(&stubWorkerMail{})
	env := map[string]string{
		"INBAI_QUOTA_INBOX_DAILY":         "2",
		"INBAI_QUOTA_DOMAIN_DAILY":        "2",
		"INBAI_QUOTA_WORKSPACE_DAILY":     "1",
		"INBAI_RATE_RECIPIENT_PER_MINUTE": "2",
	}
	if err := applySendLimits(svc, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	msg := mailbackend.Outbound{AccountID: "a", From: "a@one.test", To: "x@two.test", Subject: "hi"}
	if err := svc.Deliver(context.Background(), service.Caller{Operator: true}, msg, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Deliver(context.Background(), service.Caller{Operator: true}, msg, ""); !errors.Is(err, service.ErrQuota) {
		t.Fatalf("second send err %v", err)
	}
	if err := applySendLimits(svc, func(k string) string {
		if k == "INBAI_RATE_RECIPIENT_PER_MINUTE" {
			return "-1"
		}
		return ""
	}); err == nil {
		t.Fatal("expected error for negative rate limit")
	}
	if err := applySendLimits(svc, func(k string) string {
		if k == "INBAI_STORAGE_QUOTA_BYTES" {
			return "-10"
		}
		return ""
	}); err == nil {
		t.Fatal("expected error for negative storage quota")
	}
}
