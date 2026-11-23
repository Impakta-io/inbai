// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// AdoptSigningKey returns the key replicas use to sign access tokens.
// An empty explicit key creates one when the table is empty, or returns the key already stored.
// A non-empty explicit key is stored when the table is empty and must match a key that is already there.
func (p *Postgres) AdoptSigningKey(ctx context.Context, explicit []byte) ([]byte, error) {
	supplied := len(explicit) > 0
	if !supplied {
		existing, ok, err := p.signingKey(ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			return existing, nil
		}
		explicit = make([]byte, 32)
		if _, err := rand.Read(explicit); err != nil {
			return nil, err
		}
	}
	var stored []byte
	err := p.pool.QueryRow(ctx, `
INSERT INTO signing_key (id, secret) VALUES (1, $1)
ON CONFLICT (id) DO UPDATE SET secret = signing_key.secret
RETURNING secret`, explicit).Scan(&stored)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(stored, explicit) {
		if supplied {
			return nil, fmt.Errorf("signing key does not match the database")
		}
		return stored, nil
	}
	return append([]byte(nil), stored...), nil
}

func (p *Postgres) signingKey(ctx context.Context) ([]byte, bool, error) {
	var secret []byte
	err := p.pool.QueryRow(ctx, `SELECT secret FROM signing_key WHERE id = 1`).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return secret, true, nil
}

func readSigningKey(ctx context.Context, tx pgx.Tx, snap *Snapshot) error {
	var secret []byte
	err := tx.QueryRow(ctx, `SELECT secret FROM signing_key WHERE id = 1`).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	snap.SigningKey = secret
	return nil
}

func writeSigningKey(ctx context.Context, tx pgx.Tx, snap Snapshot) error {
	if snap.SigningKey == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM signing_key`); err != nil {
		return err
	}
	if len(snap.SigningKey) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO signing_key (id, secret) VALUES (1, $1)`, snap.SigningKey)
	return err
}
