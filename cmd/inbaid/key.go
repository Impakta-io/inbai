// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"github.com/Impakta-io/inbai/internal/store"
)

const ephemeralKeyNotice = "No database and no INBAI_SIGNING_KEY: the signing key lives only in this process, so access tokens stop working when it restarts. Set INBAI_SIGNING_KEY to at least 32 bytes to keep them."

// resolveSigningKey returns the key access tokens are signed with.
// A database stores the key so another process can verify those tokens.
// ephemeral is true only when this process generated a key that is stored nowhere.
// That key is never printed. Printing it would put it in container logs, and
// without a database no second process shares the grants its tokens refer to.
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
