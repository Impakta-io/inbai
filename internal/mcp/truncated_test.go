// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

func TestGetMessageCarriesTextTruncated(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "big", Text: "abc...", TextTruncated: true}}}
	svc := service.New(mail)
	if err := svc.Grant(service.Caller{Operator: true}, "ada", "inbox-a", service.PermRead); err != nil {
		t.Fatal(err)
	}
	box := ServiceBox{Service: svc, Caller: service.Caller{Agent: "ada"}}
	got, _ := Dispatch(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_message","arguments":{"inbox":"inbox-a","messageId":"m"}}}`), box)
	if !strings.Contains(string(got), `textTruncated\":true`) {
		t.Fatalf("get %s", got)
	}
}
