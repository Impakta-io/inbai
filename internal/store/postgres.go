// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WorkspaceID is the one workspace stored on every row. There is no workspace command.
const WorkspaceID = "default"

const schema = `
CREATE TABLE IF NOT EXISTS inbox_cursor (
    inbox text PRIMARY KEY,
    address text NOT NULL DEFAULT '',
    jmap_state text NOT NULL DEFAULT '',
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE TABLE IF NOT EXISTS mail_event (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    inbox text NOT NULL,
    message_id text NOT NULL,
    kind text NOT NULL,
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE INDEX IF NOT EXISTS mail_event_inbox_id ON mail_event (inbox, id);
CREATE TABLE IF NOT EXISTS webhook_subscription (
    id text PRIMARY KEY,
    agent text NOT NULL,
    inbox text NOT NULL,
    url text NOT NULL,
    secret text NOT NULL,
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE TABLE IF NOT EXISTS webhook_delivery (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subscription_id text NOT NULL,
    event_id bigint NOT NULL,
    attempt int NOT NULL DEFAULT 0,
    next_at timestamptz NOT NULL DEFAULT now(),
    done_at timestamptz,
    failed_at timestamptz,
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE INDEX IF NOT EXISTS webhook_delivery_due ON webhook_delivery (next_at)
    WHERE done_at IS NULL AND failed_at IS NULL;
CREATE TABLE IF NOT EXISTS suppression (
    address text PRIMARY KEY,
    reason text NOT NULL,
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE TABLE IF NOT EXISTS access_grant (
    agent text NOT NULL,
    inbox text NOT NULL,
    permission text NOT NULL,
    workspace_id text NOT NULL DEFAULT 'default',
    PRIMARY KEY (agent, inbox, permission)
);
CREATE TABLE IF NOT EXISTS quota_counter (
    bucket text PRIMARY KEY,
    used int NOT NULL,
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE TABLE IF NOT EXISTS idempotency (
    kind text NOT NULL,
    key text NOT NULL,
    payload text NOT NULL DEFAULT '',
    workspace_id text NOT NULL DEFAULT 'default',
    PRIMARY KEY (kind, key)
);
CREATE TABLE IF NOT EXISTS bulk_job (
    id text PRIMARY KEY,
    status text NOT NULL,
    total int NOT NULL,
    created int NOT NULL DEFAULT 0,
    error text NOT NULL DEFAULT '',
    inboxes jsonb NOT NULL DEFAULT '[]',
    agent text NOT NULL DEFAULT '',
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE TABLE IF NOT EXISTS agent_credential (
    id text PRIMARY KEY,
    agent text NOT NULL,
    secret_hash bytea NOT NULL,
    operator boolean NOT NULL DEFAULT false,
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE TABLE IF NOT EXISTS agent_suspension (
    agent text PRIMARY KEY,
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE TABLE IF NOT EXISTS audit_event (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at timestamptz NOT NULL DEFAULT now(),
    actor text NOT NULL,
    action text NOT NULL,
    target text NOT NULL,
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE TABLE IF NOT EXISTS retention_policy (
    scope text PRIMARY KEY,
    days int NOT NULL,
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE TABLE IF NOT EXISTS sender_allowlist (
    inbox text PRIMARY KEY,
    senders jsonb NOT NULL,
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE TABLE IF NOT EXISTS signing_key (
    id int PRIMARY KEY,
    secret bytea NOT NULL,
    workspace_id text NOT NULL DEFAULT 'default',
    CONSTRAINT signing_key_singleton CHECK (id = 1)
);
CREATE TABLE IF NOT EXISTS job_queue (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}',
    unique_key text NOT NULL DEFAULT '',
    attempt int NOT NULL DEFAULT 0,
    next_at timestamptz NOT NULL DEFAULT now(),
    done_at timestamptz,
    failed_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    workspace_id text NOT NULL DEFAULT 'default'
);
CREATE INDEX IF NOT EXISTS job_queue_due ON job_queue (next_at)
    WHERE done_at IS NULL AND failed_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS job_queue_unique_open ON job_queue (kind, unique_key)
    WHERE unique_key <> '' AND done_at IS NULL AND failed_at IS NULL;
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
	if err := ensureWorkspace(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	if err := ensureColumns(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

var workspaceTables = []string{
	"inbox_cursor",
	"mail_event",
	"webhook_subscription",
	"webhook_delivery",
	"suppression",
	"access_grant",
	"quota_counter",
	"idempotency",
	"bulk_job",
	"agent_credential",
	"agent_suspension",
	"audit_event",
	"retention_policy",
	"sender_allowlist",
	"signing_key",
	"job_queue",
}

func ensureWorkspace(ctx context.Context, pool *pgxpool.Pool) error {
	for _, table := range workspaceTables {
		stmt := fmt.Sprintf(
			`ALTER TABLE %s ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL DEFAULT '%s'`,
			table, WorkspaceID,
		)
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// addedColumns are columns added to a table after it was first created.
// CREATE TABLE IF NOT EXISTS skips a table that exists, so Open adds them to an older database.
var addedColumns = []string{
	`ALTER TABLE bulk_job ADD COLUMN IF NOT EXISTS agent text NOT NULL DEFAULT ''`,
}

func ensureColumns(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range addedColumns {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
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

// Forget drops the cursor row for an inbox, which also removes its address binding.
// Events already logged stay.
func (p *Postgres) Forget(ctx context.Context, inbox string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM inbox_cursor WHERE inbox = $1`, inbox)
	return err
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

// Head returns the id of the newest event for an inbox, or 0 when it has none.
func (p *Postgres) Head(ctx context.Context, inbox string) (int64, error) {
	var id int64
	err := p.pool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM mail_event WHERE inbox = $1`, inbox).Scan(&id)
	return id, err
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

func (p *Postgres) Suppress(ctx context.Context, address, reason string) error {
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" || !strings.Contains(address, "@") {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO suppression (address, reason) VALUES ($1, $2) ON CONFLICT (address) DO NOTHING`, address, reason)
	return err
}

func (p *Postgres) Suppressed(ctx context.Context, address string) (string, bool, error) {
	var reason string
	err := p.pool.QueryRow(ctx, `SELECT reason FROM suppression WHERE address = $1`, strings.ToLower(strings.TrimSpace(address))).Scan(&reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return reason, true, nil
}

func (p *Postgres) Suppressions(ctx context.Context) ([]Suppression, error) {
	rows, err := p.pool.Query(ctx, `SELECT address, reason FROM suppression ORDER BY address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Suppression
	for rows.Next() {
		var item Suppression
		if err := rows.Scan(&item.Address, &item.Reason); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Export copies the inbAI tables in one transaction. Message bodies are not included.
func (p *Postgres) Export(ctx context.Context) (Snapshot, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snap := emptySnapshot()
	rows, err := tx.Query(ctx, `SELECT inbox, address, jmap_state FROM inbox_cursor ORDER BY inbox`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var cur CursorRow
		if err := rows.Scan(&cur.Inbox, &cur.Address, &cur.State); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		snap.Cursors = append(snap.Cursors, cur)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Snapshot{}, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id, inbox, message_id, kind FROM mail_event ORDER BY id`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var event Record
		if err := rows.Scan(&event.ID, &event.Inbox, &event.MessageID, &event.Kind); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		snap.Events = append(snap.Events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Snapshot{}, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id, agent, inbox, url, secret FROM webhook_subscription ORDER BY id`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.ID, &sub.Agent, &sub.Inbox, &sub.URL, &sub.Secret); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		snap.Subscriptions = append(snap.Subscriptions, sub)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Snapshot{}, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id, subscription_id, event_id, attempt, next_at, done_at IS NOT NULL, failed_at IS NOT NULL FROM webhook_delivery ORDER BY id`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var item DeliveryRow
		if err := rows.Scan(&item.ID, &item.SubscriptionID, &item.EventID, &item.Attempt, &item.Next, &item.Done, &item.Failed); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		snap.Deliveries = append(snap.Deliveries, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Snapshot{}, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT address, reason FROM suppression ORDER BY address`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var item Suppression
		if err := rows.Scan(&item.Address, &item.Reason); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		snap.Suppressions = append(snap.Suppressions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Snapshot{}, err
	}
	rows.Close()
	if err := readGrants(ctx, tx, &snap); err != nil {
		return Snapshot{}, err
	}
	if err := readAudit(ctx, tx, &snap); err != nil {
		return Snapshot{}, err
	}
	if err := readRetention(ctx, tx, &snap); err != nil {
		return Snapshot{}, err
	}
	if err := readAllowlist(ctx, tx, &snap); err != nil {
		return Snapshot{}, err
	}
	if err := readSigningKey(ctx, tx, &snap); err != nil {
		return Snapshot{}, err
	}
	if err := readQueue(ctx, tx, &snap); err != nil {
		return Snapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// Import replaces the inbAI tables with the snapshot.
func (p *Postgres) Import(ctx context.Context, snap Snapshot) error {
	if err := snap.check(); err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `TRUNCATE webhook_delivery, webhook_subscription, mail_event, inbox_cursor, suppression`); err != nil {
		return err
	}
	for _, cur := range snap.Cursors {
		if _, err := tx.Exec(ctx, `INSERT INTO inbox_cursor (inbox, address, jmap_state) VALUES ($1, $2, $3)`, cur.Inbox, strings.ToLower(strings.TrimSpace(cur.Address)), cur.State); err != nil {
			return err
		}
	}
	for _, event := range snap.Events {
		if _, err := tx.Exec(ctx, `INSERT INTO mail_event (id, inbox, message_id, kind) OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4)`, event.ID, event.Inbox, event.MessageID, event.Kind); err != nil {
			return err
		}
	}
	for _, sub := range snap.Subscriptions {
		if _, err := tx.Exec(ctx, `INSERT INTO webhook_subscription (id, agent, inbox, url, secret) VALUES ($1, $2, $3, $4, $5)`, sub.ID, sub.Agent, sub.Inbox, sub.URL, sub.Secret); err != nil {
			return err
		}
	}
	for _, item := range snap.Deliveries {
		var done, failed any
		if item.Done {
			done = item.Next
		}
		if item.Failed {
			failed = item.Next
		}
		if _, err := tx.Exec(ctx, `INSERT INTO webhook_delivery (id, subscription_id, event_id, attempt, next_at, done_at, failed_at) OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5, $6, $7)`, item.ID, item.SubscriptionID, item.EventID, item.Attempt, item.Next, done, failed); err != nil {
			return err
		}
	}
	for _, item := range snap.Suppressions {
		if _, err := tx.Exec(ctx, `INSERT INTO suppression (address, reason) VALUES ($1, $2)`, strings.ToLower(strings.TrimSpace(item.Address)), item.Reason); err != nil {
			return err
		}
	}
	if err := writeGrants(ctx, tx, snap); err != nil {
		return err
	}
	if err := writeAudit(ctx, tx, snap); err != nil {
		return err
	}
	if err := writeRetention(ctx, tx, snap); err != nil {
		return err
	}
	if err := writeAllowlist(ctx, tx, snap); err != nil {
		return err
	}
	if err := writeSigningKey(ctx, tx, snap); err != nil {
		return err
	}
	if err := writeQueue(ctx, tx, snap); err != nil {
		return err
	}
	if err := setIdentity(ctx, tx, "mail_event", "id", maxID(snap.Events)); err != nil {
		return err
	}
	if err := setIdentity(ctx, tx, "webhook_delivery", "id", maxDelivery(snap.Deliveries)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func maxID(events []Record) int64 {
	var max int64
	for _, event := range events {
		if event.ID > max {
			max = event.ID
		}
	}
	return max
}

func maxDelivery(items []DeliveryRow) int64 {
	var max int64
	for _, item := range items {
		if item.ID > max {
			max = item.ID
		}
	}
	return max
}

func setIdentity(ctx context.Context, tx pgx.Tx, table, column string, max int64) error {
	called := max > 0
	if max == 0 {
		max = 1
	}
	_, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1, $2), $3, $4)`, table, column, max, called)
	return err
}

func readGrants(ctx context.Context, tx pgx.Tx, snap *Snapshot) error {
	rows, err := tx.Query(ctx, `SELECT agent, inbox, permission FROM access_grant ORDER BY agent, inbox, permission`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row GrantRow
		if err := rows.Scan(&row.Agent, &row.Inbox, &row.Permission); err != nil {
			rows.Close()
			return err
		}
		snap.Grants = append(snap.Grants, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT bucket, used FROM quota_counter ORDER BY bucket`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row QuotaRow
		if err := rows.Scan(&row.Bucket, &row.Used); err != nil {
			rows.Close()
			return err
		}
		snap.Quotas = append(snap.Quotas, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT kind, key, payload FROM idempotency ORDER BY kind, key`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row IdempotencyRow
		if err := rows.Scan(&row.Kind, &row.Key, &row.Payload); err != nil {
			rows.Close()
			return err
		}
		snap.Idempotency = append(snap.Idempotency, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id, status, total, created, error, inboxes, agent FROM bulk_job ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row JobSnap
		if err := rows.Scan(&row.ID, &row.Status, &row.Total, &row.Created, &row.Error, &row.Inboxes, &row.Agent); err != nil {
			rows.Close()
			return err
		}
		snap.Jobs = append(snap.Jobs, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id, agent, secret_hash, operator FROM agent_credential ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row ClientRow
		if err := rows.Scan(&row.ID, &row.Agent, &row.SecretHash, &row.Operator); err != nil {
			rows.Close()
			return err
		}
		snap.Clients = append(snap.Clients, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT agent FROM agent_suspension ORDER BY agent`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var agent string
		if err := rows.Scan(&agent); err != nil {
			rows.Close()
			return err
		}
		snap.Suspensions = append(snap.Suspensions, agent)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	return nil
}

func readAudit(ctx context.Context, tx pgx.Tx, snap *Snapshot) error {
	rows, err := tx.Query(ctx, `SELECT id, at, actor, action, target FROM audit_event ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row AuditRow
		if err := rows.Scan(&row.ID, &row.At, &row.Actor, &row.Action, &row.Target); err != nil {
			return err
		}
		snap.Audits = append(snap.Audits, row)
	}
	return rows.Err()
}

func writeAudit(ctx context.Context, tx pgx.Tx, snap Snapshot) error {
	if snap.Audits == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `TRUNCATE audit_event`); err != nil {
		return err
	}
	var max int64
	for _, row := range snap.Audits {
		if _, err := tx.Exec(ctx, `INSERT INTO audit_event (id, at, actor, action, target) OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5)`, row.ID, row.At, row.Actor, row.Action, row.Target); err != nil {
			return err
		}
		if row.ID > max {
			max = row.ID
		}
	}
	return setIdentity(ctx, tx, "audit_event", "id", max)
}

func readRetention(ctx context.Context, tx pgx.Tx, snap *Snapshot) error {
	rows, err := tx.Query(ctx, `SELECT scope, days FROM retention_policy ORDER BY scope`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row RetentionRow
		if err := rows.Scan(&row.Scope, &row.Days); err != nil {
			return err
		}
		snap.Retention = append(snap.Retention, row)
	}
	return rows.Err()
}

func writeRetention(ctx context.Context, tx pgx.Tx, snap Snapshot) error {
	if snap.Retention == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `TRUNCATE retention_policy`); err != nil {
		return err
	}
	for _, row := range snap.Retention {
		if _, err := tx.Exec(ctx, `INSERT INTO retention_policy (scope, days) VALUES ($1, $2)`, row.Scope, row.Days); err != nil {
			return err
		}
	}
	return nil
}

func writeGrants(ctx context.Context, tx pgx.Tx, snap Snapshot) error {
	if snap.Grants == nil && snap.Quotas == nil && snap.Idempotency == nil && snap.Jobs == nil && snap.Clients == nil && snap.Suspensions == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `TRUNCATE access_grant, quota_counter, idempotency, bulk_job, agent_credential, agent_suspension`); err != nil {
		return err
	}
	for _, row := range snap.Grants {
		if _, err := tx.Exec(ctx, `INSERT INTO access_grant (agent, inbox, permission) VALUES ($1, $2, $3)`, row.Agent, row.Inbox, row.Permission); err != nil {
			return err
		}
	}
	for _, row := range snap.Quotas {
		if _, err := tx.Exec(ctx, `INSERT INTO quota_counter (bucket, used) VALUES ($1, $2)`, row.Bucket, row.Used); err != nil {
			return err
		}
	}
	for _, row := range snap.Idempotency {
		if _, err := tx.Exec(ctx, `INSERT INTO idempotency (kind, key, payload) VALUES ($1, $2, $3)`, row.Kind, row.Key, row.Payload); err != nil {
			return err
		}
	}
	for _, row := range snap.Jobs {
		inboxes := row.Inboxes
		if len(inboxes) == 0 {
			inboxes = []byte("[]")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bulk_job (id, status, total, created, error, inboxes, agent) VALUES ($1, $2, $3, $4, $5, $6, $7)`, row.ID, row.Status, row.Total, row.Created, row.Error, inboxes, row.Agent); err != nil {
			return err
		}
	}
	for _, row := range snap.Clients {
		if _, err := tx.Exec(ctx, `INSERT INTO agent_credential (id, agent, secret_hash, operator) VALUES ($1, $2, $3, $4)`, row.ID, row.Agent, row.SecretHash, row.Operator); err != nil {
			return err
		}
	}
	for _, agent := range snap.Suspensions {
		if _, err := tx.Exec(ctx, `INSERT INTO agent_suspension (agent) VALUES ($1)`, agent); err != nil {
			return err
		}
	}
	return nil
}
