// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"

	"errors"

	"github.com/jackc/pgx/v5"
)

// AllowlistRow is the sender allowlist for one inbox.
type AllowlistRow struct {
	Inbox   string   `json:"inbox"`
	Senders []string `json:"senders"`
}

// SaveAllowlist replaces the senders for one inbox.
func (p *Postgres) SaveAllowlist(ctx context.Context, inbox string, senders []string) error {
	raw, err := json.Marshal(senders)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `
INSERT INTO sender_allowlist (inbox, senders) VALUES ($1, $2)
ON CONFLICT (inbox) DO UPDATE SET senders = EXCLUDED.senders`, inbox, raw)
	return err
}

// DeleteAllowlist removes the list for one inbox.
func (p *Postgres) DeleteAllowlist(ctx context.Context, inbox string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sender_allowlist WHERE inbox = $1`, inbox)
	return err
}

// Allowlist returns the stored senders. ok is false when the inbox has no row.
func (p *Postgres) Allowlist(ctx context.Context, inbox string) ([]string, bool, error) {
	var raw []byte
	err := p.pool.QueryRow(ctx, `SELECT senders FROM sender_allowlist WHERE inbox = $1`, inbox).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var senders []string
	if err := json.Unmarshal(raw, &senders); err != nil {
		return nil, false, err
	}
	return senders, true, nil
}

func readAllowlist(ctx context.Context, tx pgx.Tx, snap *Snapshot) error {
	snap.Allowlists = []AllowlistRow{}
	rows, err := tx.Query(ctx, `SELECT inbox, senders FROM sender_allowlist ORDER BY inbox`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row AllowlistRow
		var raw []byte
		if err := rows.Scan(&row.Inbox, &raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &row.Senders); err != nil {
			return err
		}
		snap.Allowlists = append(snap.Allowlists, row)
	}
	return rows.Err()
}

func writeAllowlist(ctx context.Context, tx pgx.Tx, snap Snapshot) error {
	if snap.Allowlists == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `TRUNCATE sender_allowlist`); err != nil {
		return err
	}
	for _, row := range snap.Allowlists {
		raw, err := json.Marshal(row.Senders)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sender_allowlist (inbox, senders) VALUES ($1, $2)`, row.Inbox, raw); err != nil {
			return err
		}
	}
	return nil
}
