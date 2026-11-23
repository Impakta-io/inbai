// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"
)

func TestResolveSigningKeyPrintsOnlyAGeneratedKey(t *testing.T) {
	key, show, err := resolveSigningKey(context.Background(), nil, []byte("configured"))
	if err != nil || show || string(key) != "configured" {
		t.Fatalf("key %q show %v err %v", key, show, err)
	}
	generated, show, err := resolveSigningKey(context.Background(), nil, nil)
	if err != nil || !show || len(generated) != 32 {
		t.Fatalf("generated %d show %v err %v", len(generated), show, err)
	}
}
