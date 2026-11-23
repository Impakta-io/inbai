// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"strings"
	"time"

	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE TABLE IF NOT EXISTS inbox_cursor (
    inbox text PRIMARY KEY,
    address text NOT NULL DEFAULT '',
    jmap_state text NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS mail_event (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    inbox text NOT NULL,
    message_id text NOT NULL,
    kind text NOT NULL
);
CREATE INDEX IF NOT EXISTS mail_event_inbox_id ON mail_event (inbox, id);
CREATE TABLE IF NOT EXISTS webhook_subscription (
    id text PRIMARY KEY,
    agent text NOT NULL,
    inbox text NOT NULL,
    url text NOT NULL,
    secret text NOT NULL
);
CREATE TABLE IF NOT EXISTS webhook_delivery (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subscription_id text NOT NULL,
    event_id bigint NOT NULL,
    attempt int NOT NULL DEFAULT 0,
    next_at timestamptz NOT NULL DEFAULT now(),
    done_at timestamptz,
    failed_at timestamptz
);
CREATE INDEX IF NOT EXISTS webhook_delivery_due ON webhook_delivery (next_at)
    WHERE done_at IS NULL AND failed_at IS NULL;
`

// Postgres is the event log that survives an inbaid restart.
type Postgres struct {
	pool *pgxpool.Pool
}

// Open connects to PostgreSQL and creates the event tables.
func Open(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

// Close releases the pool.
func (p *Postgres) Close() {
	p.pool.Close()
}

func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

func (p *Postgres) Remember(ctx context.Context, inbox, address string) error {
	address = strings.ToLower(strings.TrimSpace(address))
	_, err := p.pool.Exec(ctx, `
INSERT INTO inbox_cursor (inbox, address, jmap_state)
VALUES ($1, $2, '')
ON CONFLICT (inbox) DO UPDATE SET address = EXCLUDED.address`, inbox, address)
	return err
}

func (p *Postgres) State(ctx context.Context, inbox string) (string, bool, error) {
	var state string
	err := p.pool.QueryRow(ctx, `SELECT jmap_state FROM inbox_cursor WHERE inbox = $1 AND jmap_state <> ''`, inbox).Scan(&state)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return state, true, nil
}

func (p *Postgres) InboxByAddress(ctx context.Context, address string) (string, bool, error) {
	var inbox string
	err := p.pool.QueryRow(ctx, `SELECT inbox FROM inbox_cursor WHERE address = $1`, strings.ToLower(strings.TrimSpace(address))).Scan(&inbox)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return inbox, true, nil
}

func (p *Postgres) Inboxes(ctx context.Context) ([]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT inbox FROM inbox_cursor ORDER BY inbox`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var inbox string
		if err := rows.Scan(&inbox); err != nil {
			return nil, err
		}
		out = append(out, inbox)
	}
	return out, rows.Err()
}

func (p *Postgres) Put(ctx context.Context, inbox, address, state string, records []Record) error {
	address = strings.ToLower(strings.TrimSpace(address))
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO inbox_cursor (inbox, address, jmap_state)
VALUES ($1, $2, $3)
ON CONFLICT (inbox) DO UPDATE SET
    jmap_state = CASE WHEN EXCLUDED.jmap_state = '' THEN inbox_cursor.jmap_state ELSE EXCLUDED.jmap_state END,
    address = CASE WHEN EXCLUDED.address = '' THEN inbox_cursor.address ELSE EXCLUDED.address END`, inbox, address, state); err != nil {
		return err
	}
	for _, record := range records {
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO mail_event (inbox, message_id, kind) VALUES ($1, $2, $3) RETURNING id`, inbox, record.MessageID, record.Kind).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO webhook_delivery (subscription_id, event_id)
SELECT id, $2 FROM webhook_subscription WHERE inbox = $1`, inbox, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Since(ctx context.Context, inbox string, after int64, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `
SELECT id, inbox, message_id, kind FROM mail_event
WHERE inbox = $1 AND id > $2 ORDER BY id LIMIT $3`, inbox, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.ID, &record.Inbox, &record.MessageID, &record.Kind); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (p *Postgres) Subscribe(ctx context.Context, sub Subscription) error {
	_, err := p.pool.Exec(ctx, `
INSERT INTO webhook_subscription (id, agent, inbox, url, secret) VALUES ($1, $2, $3, $4, $5)`,
		sub.ID, sub.Agent, sub.Inbox, sub.URL, sub.Secret)
	return err
}

func (p *Postgres) Claim(ctx context.Context, now time.Time) (Delivery, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Delivery{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var delivery Delivery
	err = tx.QueryRow(ctx, `
WITH claimed AS (
    SELECT id FROM webhook_delivery
    WHERE done_at IS NULL AND failed_at IS NULL AND next_at <= $1
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
), updated AS (
    UPDATE webhook_delivery AS delivery
    SET attempt = delivery.attempt + 1, next_at = $1 + interval '30 seconds'
    FROM claimed
    WHERE delivery.id = claimed.id
    RETURNING delivery.id, delivery.attempt, delivery.subscription_id, delivery.event_id
)
SELECT updated.id, updated.attempt, subscription.url, subscription.secret,
       event.id, event.inbox, event.message_id, event.kind
FROM updated
JOIN webhook_subscription AS subscription ON subscription.id = updated.subscription_id
JOIN mail_event AS event ON event.id = updated.event_id`, now).Scan(
		&delivery.ID, &delivery.Attempt, &delivery.URL, &delivery.Secret,
		&delivery.Event.ID, &delivery.Event.Inbox, &delivery.Event.MessageID, &delivery.Event.Kind,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Delivery{}, false, nil
		}
		return Delivery{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Delivery{}, false, err
	}
	return delivery, true, nil
}

func (p *Postgres) Ack(ctx context.Context, id int64, now time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE webhook_delivery SET done_at = $2 WHERE id = $1`, id, now)
	return err
}

func (p *Postgres) Retry(ctx context.Context, id int64, next time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE webhook_delivery SET next_at = $2 WHERE id = $1`, id, next)
	return err
}

func (p *Postgres) Fail(ctx context.Context, id int64, now time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE webhook_delivery SET failed_at = $2 WHERE id = $1`, id, now)
	return err
}
