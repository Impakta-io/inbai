// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"github.com/Impakta-io/inbai/internal/config"
)

func TestResolveSigningKeyPrintsOnlyAGeneratedKey(t *testing.T) {
	key, show, err := resolveSigningKey(context.Background(), nil, []byte("configured-0123456789abcdef0123456789"))
	if err != nil || show || string(key) != "configured-0123456789abcdef0123456789" {
		t.Fatalf("key %q show %v err %v", key, show, err)
	}
	generated, show, err := resolveSigningKey(context.Background(), nil, nil)
	if err != nil || !show || len(generated) != 32 {
		t.Fatalf("generated %d show %v err %v", len(generated), show, err)
	}
}

func TestGeneratedSigningKeyMeetsTheMinimum(t *testing.T) {
	key, _, err := resolveSigningKey(context.Background(), nil, nil)
	if err != nil || config.CheckSigningKey(key) != nil {
		t.Fatalf("generated key %d bytes err %v", len(key), err)
	}
}
