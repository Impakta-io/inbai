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
    operator boolean NOT NULL DEFAULT false,
    disabled text NOT NULL DEFAULT '',
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
    fail_reason text NOT NULL DEFAULT '',
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
    state text NOT NULL DEFAULT 'done',
    request_hash text NOT NULL DEFAULT '',
    reserved_at timestamptz NOT NULL DEFAULT now(),
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
//
// webhook_subscription.operator is added empty and filled only where it is empty, so a later
// Open does not change a row. A subscription stored before then counts as an operator's when
// its agent is legacyOperatorAgent, the name the server stored for operator subscriptions.
var addedColumns = []string{
	`ALTER TABLE bulk_job ADD COLUMN IF NOT EXISTS agent text NOT NULL DEFAULT ''`,
	`ALTER TABLE webhook_subscription ADD COLUMN IF NOT EXISTS operator boolean`,
	`UPDATE webhook_subscription SET operator = (agent = '` + legacyOperatorAgent + `') WHERE operator IS NULL`,
	`ALTER TABLE webhook_subscription ALTER COLUMN operator SET DEFAULT false`,
	`ALTER TABLE webhook_subscription ALTER COLUMN operator SET NOT NULL`,
	`ALTER TABLE webhook_subscription ADD COLUMN IF NOT EXISTS disabled text NOT NULL DEFAULT ''`,
	`ALTER TABLE webhook_delivery ADD COLUMN IF NOT EXISTS fail_reason text NOT NULL DEFAULT ''`,
	`ALTER TABLE idempotency ADD COLUMN IF NOT EXISTS state text NOT NULL DEFAULT 'done'`,
	`ALTER TABLE idempotency ADD COLUMN IF NOT EXISTS request_hash text NOT NULL DEFAULT ''`,
	`ALTER TABLE idempotency ADD COLUMN IF NOT EXISTS reserved_at timestamptz NOT NULL DEFAULT now()`,
	// Webhook delivery and pruning. mail_event.at is when the event was logged; an event stored
	// before the column existed counts from the upgrade. sending_delivery and sending_until mark
	// the one delivery in flight for a subscription until it finishes or its lease ends.
	`ALTER TABLE mail_event ADD COLUMN IF NOT EXISTS at timestamptz NOT NULL DEFAULT now()`,
	`ALTER TABLE webhook_subscription ADD COLUMN IF NOT EXISTS sending_delivery bigint`,
	`ALTER TABLE webhook_subscription ADD COLUMN IF NOT EXISTS sending_until timestamptz`,
	`CREATE INDEX IF NOT EXISTS webhook_delivery_event ON webhook_delivery (event_id)`,
	`CREATE INDEX IF NOT EXISTS webhook_delivery_pending ON webhook_delivery (subscription_id, id)
    WHERE done_at IS NULL AND failed_at IS NULL`,
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

// InboxKeys names the rows ForgetInbox removes for one inbox. Inbox is the account id.
// RetentionScope is that inbox's retention_policy scope. CreateKind is the idempotency
// kind whose payload is the id of the inbox a create made. internal/service chooses both.
type InboxKeys struct {
	Inbox          string
	RetentionScope string
	CreateKind     string
}

// ForgetInbox removes, in one transaction, what is stored for an inbox that was deleted:
// its cursor and address binding, its webhook subscriptions and every delivery queued for
// them, every agent's grants on it, its sender allowlist, its retention policy, and the
// idempotency keys of creates that returned its id. Events and audit rows stay.
// Rows already gone are not an error, so a failed call can be repeated.
//
// The subscriptions are deleted before their deliveries for the reason Unsubscribe gives.
func (p *Postgres) ForgetInbox(ctx context.Context, keys InboxKeys) error {
	if keys.Inbox == "" {
		return errors.New("forget inbox: inbox id is required")
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `DELETE FROM webhook_subscription WHERE inbox = $1 RETURNING id`, keys.Inbox)
	if err != nil {
		return err
	}
	subs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(subs) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM webhook_delivery WHERE subscription_id = ANY($1)`, subs); err != nil {
			return err
		}
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM inbox_cursor WHERE inbox = $1`, []any{keys.Inbox}},
		{`DELETE FROM access_grant WHERE inbox = $1`, []any{keys.Inbox}},
		{`DELETE FROM sender_allowlist WHERE inbox = $1`, []any{keys.Inbox}},
		{`DELETE FROM retention_policy WHERE scope = $1`, []any{keys.RetentionScope}},
		{`DELETE FROM idempotency WHERE kind = $1 AND payload = $2`, []any{keys.CreateKind, keys.Inbox}},
	} {
		if _, err := tx.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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
		// FOR SHARE waits for an Unsubscribe or Disable of the same row and then skips it,
		// so no delivery is queued for a subscription that was just removed or stopped.
		if _, err := tx.Exec(ctx, `
INSERT INTO webhook_delivery (subscription_id, event_id)
SELECT id, $2 FROM webhook_subscription WHERE inbox = $1 AND disabled = '' FOR SHARE`, inbox, id); err != nil {
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

func (p *Postgres) Subscribe(ctx context.Context, sub Subscription, limit int) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if limit > 0 && !sub.Operator {
		// The lock serialises two creates for the same agent and inbox, so both cannot pass the count.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('webhook_subscription'), hashtext($1))`, sub.Agent+"\n"+sub.Inbox); err != nil {
			return err
		}
		var held int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM webhook_subscription WHERE agent = $1 AND inbox = $2 AND NOT operator`, sub.Agent, sub.Inbox).Scan(&held); err != nil {
			return err
		}
		if held >= limit {
			return ErrSubscriptionLimit
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO webhook_subscription (id, agent, inbox, url, secret, operator, disabled) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		sub.ID, sub.Agent, sub.Inbox, sub.URL, sub.Secret, sub.Operator, sub.Disabled); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const subscriptionColumns = `id, agent, inbox, url, secret, operator, disabled`

func scanSubscription(row pgx.Row, sub *Subscription) error {
	return row.Scan(&sub.ID, &sub.Agent, &sub.Inbox, &sub.URL, &sub.Secret, &sub.Operator, &sub.Disabled)
}

func (p *Postgres) Subscription(ctx context.Context, id string) (Subscription, bool, error) {
	var sub Subscription
	err := scanSubscription(p.pool.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM webhook_subscription WHERE id = $1`, id), &sub)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, false, nil
	}
	if err != nil {
		return Subscription{}, false, err
	}
	return sub, true, nil
}

func (p *Postgres) Subscriptions(ctx context.Context, inbox string) ([]Subscription, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+subscriptionColumns+` FROM webhook_subscription WHERE inbox = $1 ORDER BY id`, inbox)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var sub Subscription
		if err := scanSubscription(rows, &sub); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// Unsubscribe deletes the subscription row first. That waits for a Put holding it, so the
// delivery delete that follows also removes what that Put queued.
func (p *Postgres) Unsubscribe(ctx context.Context, id string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM webhook_subscription WHERE id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM webhook_delivery WHERE subscription_id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Disable marks the subscription first, for the same reason as Unsubscribe, then fails
// every delivery still pending for it with the reason.
func (p *Postgres) Disable(ctx context.Context, id, reason string, now time.Time) error {
	if reason == "" {
		reason = "disabled"
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE webhook_subscription SET disabled = $2 WHERE id = $1 AND disabled = ''`, id, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
UPDATE webhook_delivery SET failed_at = $3, fail_reason = $2
WHERE subscription_id = $1 AND done_at IS NULL AND failed_at IS NULL`, id, reason, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Claim leases the oldest due delivery whose subscription has none in flight, and marks
// the subscription busy until the delivery finishes or the lease ends.
//
// Only the subscription row is locked, with SKIP LOCKED, and only a subscription's oldest
// due delivery is a candidate, so a claim never skips ahead within one subscription.
// When another process marked the subscription first, the lock sees the new row and its
// sending_until fails the condition, so two processes never send for one subscription at
// the same time.
//
// The lock rechecks only the subscription row. Under READ COMMITTED the delivery columns
// in claimed still come from the statement's snapshot, so a delivery another process
// claimed and finished after that snapshot can look pending although its subscription
// is free again. updated therefore repeats the delivery conditions. An UPDATE evaluates
// its WHERE on the newest committed version of the row, waiting for a writer still in
// progress, so a delivery that was finished or rescheduled meanwhile matches nothing.
// busy marks the subscription only for a row updated returned, so a stale candidate
// claims nothing and leaves the subscription as it was; the next claim reads afresh.
// No earlier delivery can have become due in between: a finished one stays finished, and
// one waiting for a retry keeps a later next_at.
func (p *Postgres) Claim(ctx context.Context, now time.Time) (Delivery, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Delivery{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var delivery Delivery
	err = tx.QueryRow(ctx, `
WITH claimed AS (
    SELECT delivery.id, delivery.subscription_id FROM webhook_delivery AS delivery
    JOIN webhook_subscription AS subscription ON subscription.id = delivery.subscription_id
    WHERE delivery.done_at IS NULL AND delivery.failed_at IS NULL AND delivery.next_at <= $1
      AND (subscription.sending_until IS NULL OR subscription.sending_until <= $1)
      AND NOT EXISTS (
          SELECT 1 FROM webhook_delivery AS earlier
          WHERE earlier.subscription_id = delivery.subscription_id AND earlier.id < delivery.id
            AND earlier.done_at IS NULL AND earlier.failed_at IS NULL AND earlier.next_at <= $1)
    ORDER BY delivery.id
    FOR UPDATE OF subscription SKIP LOCKED
    LIMIT 1
), updated AS (
    UPDATE webhook_delivery AS delivery
    SET attempt = delivery.attempt + 1, next_at = $2
    FROM claimed
    WHERE delivery.id = claimed.id
      AND delivery.done_at IS NULL AND delivery.failed_at IS NULL AND delivery.next_at <= $1
    RETURNING delivery.id, delivery.attempt, delivery.subscription_id, delivery.event_id
), busy AS (
    UPDATE webhook_subscription AS subscription
    SET sending_delivery = updated.id, sending_until = $2
    FROM updated
    WHERE subscription.id = updated.subscription_id
)
SELECT updated.id, updated.attempt, subscription.id, subscription.agent, subscription.operator,
       subscription.url, subscription.secret,
       event.id, event.inbox, event.message_id, event.kind
FROM updated
JOIN webhook_subscription AS subscription ON subscription.id = updated.subscription_id
JOIN mail_event AS event ON event.id = updated.event_id`, now, now.Add(claimLease)).Scan(
		&delivery.ID, &delivery.Attempt, &delivery.SubscriptionID, &delivery.Agent, &delivery.Operator,
		&delivery.URL, &delivery.Secret,
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

// Ack records that the receiver took the delivery. That stands even when the delivery had
// failed meanwhile, for example because its subscription was disabled during the post.
func (p *Postgres) Ack(ctx context.Context, id int64, now time.Time) error {
	return p.finish(ctx, id, now, `
UPDATE webhook_delivery SET done_at = $2, failed_at = NULL, fail_reason = '' WHERE id = $1 AND done_at IS NULL`)
}

// Retry schedules a delivery that has not finished.
func (p *Postgres) Retry(ctx context.Context, id int64, next time.Time) error {
	return p.finish(ctx, id, next, `
UPDATE webhook_delivery SET next_at = $2 WHERE id = $1 AND done_at IS NULL AND failed_at IS NULL`)
}

// Fail drops a delivery that has not finished.
func (p *Postgres) Fail(ctx context.Context, id int64, now time.Time) error {
	return p.finish(ctx, id, now, `
UPDATE webhook_delivery SET failed_at = $2 WHERE id = $1 AND done_at IS NULL AND failed_at IS NULL`)
}

// finish records a claimed delivery's outcome with change, whose parameters are the
// delivery id and a time, and clears the subscription's in-flight mark when it names this
// delivery. The outcome is recorded whether or not the mark still names it: after a lease
// ended, another claim may have taken the subscription, and its mark is left alone.
//
// The subscription row is locked first, as Claim, Disable and Unsubscribe lock it before
// delivery rows, so finishing a delivery whose lease ended never deadlocks with a claim
// of it. The statement after the lock reads every change committed before it.
func (p *Postgres) finish(ctx context.Context, id int64, at time.Time, change string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sub string
	err = tx.QueryRow(ctx, `
SELECT subscription.id FROM webhook_subscription AS subscription
JOIN webhook_delivery AS delivery ON delivery.subscription_id = subscription.id
WHERE delivery.id = $1
FOR UPDATE OF subscription`, id).Scan(&sub)
	if errors.Is(err, pgx.ErrNoRows) {
		// The delivery or its subscription was removed.
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
WITH changed AS (`+change+`)
UPDATE webhook_subscription SET sending_delivery = NULL, sending_until = NULL
WHERE id = $3 AND sending_delivery = $1`, id, at, sub); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
	rows, err = tx.Query(ctx, `SELECT id, inbox, message_id, kind, at FROM mail_event ORDER BY id`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var event EventRow
		if err := rows.Scan(&event.ID, &event.Inbox, &event.MessageID, &event.Kind, &event.At); err != nil {
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
	// The sequence outlives pruned events. Restoring it keeps every consumer cursor behind new ids.
	if err := tx.QueryRow(ctx, `SELECT COALESCE(pg_sequence_last_value(pg_get_serial_sequence('mail_event', 'id')::regclass), 0)`).Scan(&snap.EventSequence); err != nil {
		return Snapshot{}, err
	}
	rows, err = tx.Query(ctx, `SELECT `+subscriptionColumns+` FROM webhook_subscription ORDER BY id`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var sub Subscription
		if err := scanSubscription(rows, &sub); err != nil {
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
	rows, err = tx.Query(ctx, `SELECT id, subscription_id, event_id, attempt, next_at, done_at IS NOT NULL, failed_at IS NOT NULL, fail_reason FROM webhook_delivery ORDER BY id`)
	if err != nil {
		return Snapshot{}, err
	}
	for rows.Next() {
		var item DeliveryRow
		if err := rows.Scan(&item.ID, &item.SubscriptionID, &item.EventID, &item.Attempt, &item.Next, &item.Done, &item.Failed, &item.Reason); err != nil {
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
		// A file written before events had a time leaves at empty, and the event counts from the restore.
		var at any
		if !event.At.IsZero() {
			at = event.At
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_event (id, inbox, message_id, kind, at) OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, COALESCE($5::timestamptz, now()))`, event.ID, event.Inbox, event.MessageID, event.Kind, at); err != nil {
			return err
		}
	}
	for _, sub := range snap.Subscriptions {
		if _, err := tx.Exec(ctx, `INSERT INTO webhook_subscription (`+subscriptionColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7)`, sub.ID, sub.Agent, sub.Inbox, sub.URL, sub.Secret, sub.Operator, sub.Disabled); err != nil {
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
		if _, err := tx.Exec(ctx, `INSERT INTO webhook_delivery (id, subscription_id, event_id, attempt, next_at, done_at, failed_at, fail_reason) OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, item.ID, item.SubscriptionID, item.EventID, item.Attempt, item.Next, done, failed, item.Reason); err != nil {
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
	if err := setIdentity(ctx, tx, "mail_event", "id", max(maxID(snap.Events), snap.EventSequence)); err != nil {
		return err
	}
	if err := setIdentity(ctx, tx, "webhook_delivery", "id", maxDelivery(snap.Deliveries)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func maxID(events []EventRow) int64 {
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
	rows, err = tx.Query(ctx, `SELECT kind, key, payload, state, request_hash, reserved_at FROM idempotency ORDER BY kind, key`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row IdempotencyRow
		if err := rows.Scan(&row.Kind, &row.Key, &row.Payload, &row.State, &row.RequestHash, &row.ReservedAt); err != nil {
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
		state := row.State
		if state == "" {
			state = IdempotencyDone
		}
		reserved := row.ReservedAt
		if reserved.IsZero() {
			reserved = time.Now().UTC()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idempotency (kind, key, payload, state, request_hash, reserved_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			row.Kind, row.Key, row.Payload, state, row.RequestHash, reserved); err != nil {
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
