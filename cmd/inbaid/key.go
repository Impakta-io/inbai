// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"github.com/Impakta-io/inbai/internal/store"
)

// resolveSigningKey returns the key access tokens are signed with.
// A database stores the key so another process can verify those tokens.
// show is true only when this process generated a key that is not stored.
func resolveSigningKey(ctx context.Context, db *store.Postgres, explicit []byte) ([]byte, bool, error) {
	if db != nil {
		key, err := db.AdoptSigningKey(ctx, explicit)
		return key, false, err
	}
	if len(explicit) > 0 {
		return explicit, false, nil
	}
	key, err := randomKey()
	return key, true, err
}
