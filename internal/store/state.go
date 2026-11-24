// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SaveGrant records one permission. A repeat is the same grant.
func (p *Postgres) SaveGrant(ctx context.Context, agent, inbox, permission string) error {
	_, err := p.pool.Exec(ctx, `
INSERT INTO access_grant (agent, inbox, permission) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING`, agent, inbox, permission)
	return err
}

// DeleteGrant removes one permission. A missing grant is not an error.
func (p *Postgres) DeleteGrant(ctx context.Context, agent, inbox, permission string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM access_grant WHERE agent = $1 AND inbox = $2 AND permission = $3`, agent, inbox, permission)
	return err
}
func (p *Postgres) HasGrant(ctx context.Context, agent, inbox, permission string) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM access_grant WHERE agent = $1 AND inbox = $2 AND permission = $3)`,
		agent, inbox, permission).Scan(&ok)
	return ok, err
}

// BucketLimit pairs a counter bucket key with its maximum allowed count.
type BucketLimit struct {
	Key   string
	Limit int
}

// Consume adds one to both counters when each is still under its limit.
// A rejected consume leaves both counters unchanged.
func (p *Postgres) Consume(ctx context.Context, inboxKey, domainKey string, inboxLimit, domainLimit int) (bool, error) {
	return p.ConsumeBuckets(ctx, []BucketLimit{
		{Key: inboxKey, Limit: inboxLimit},
		{Key: domainKey, Limit: domainLimit},
	})
}

// ConsumeBuckets atomically adds one to every bucket when all are under their limits.
// If any bucket is at or over its limit, none of the buckets change.
func (p *Postgres) ConsumeBuckets(ctx context.Context, buckets []BucketLimit) (bool, error) {
	for _, b := range buckets {
		if b.Limit <= 0 {
			return false, nil
		}
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, b := range buckets {
		ok, err := bump(ctx, tx, b.Key, b.Limit)
		if err != nil || !ok {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func bump(ctx context.Context, tx pgx.Tx, key string, limit int) (bool, error) {
	var used int
	err := tx.QueryRow(ctx, `
INSERT INTO quota_counter (bucket, used) VALUES ($1, 1)
ON CONFLICT (bucket) DO UPDATE SET used = quota_counter.used + 1
WHERE quota_counter.used < $2
RETURNING used`, key, limit).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Refund removes one use from each counter. A missing counter stays missing.
func (p *Postgres) Refund(ctx context.Context, inboxKey, domainKey string) error {
	return p.RefundBuckets(ctx, []string{inboxKey, domainKey})
}

// RefundBuckets removes one use from every listed counter.
func (p *Postgres) RefundBuckets(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	_, err := p.pool.Exec(ctx, `
UPDATE quota_counter SET used = GREATEST(used - 1, 0) WHERE bucket = ANY($1)`, keys)
	return err
}

// SaveIdempotency stores the first result for a key. A repeat does not replace it.
func (p *Postgres) SaveIdempotency(ctx context.Context, kind, key, payload string) error {
	_, err := p.pool.Exec(ctx, `
INSERT INTO idempotency (kind, key, payload) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING`, kind, key, payload)
	return err
}

// Idempotency returns the stored payload. ok is false when the key is new.
func (p *Postgres) Idempotency(ctx context.Context, kind, key string) (string, bool, error) {
	var payload string
	err := p.pool.QueryRow(ctx, `SELECT payload FROM idempotency WHERE kind = $1 AND key = $2`, kind, key).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return payload, err == nil, err
}

const (
	// IdempotencyInProgress is a key reserved by a request that has not finished.
	IdempotencyInProgress = "in_progress"
	// IdempotencyDone is a key whose request finished. A row written by SaveIdempotency is done.
	IdempotencyDone = "done"
)

// IdempotencyClaim is the row a ReserveIdempotency call found instead of reserving the key.
type IdempotencyClaim struct {
	State       string
	RequestHash string
	Payload     string
}

// ReserveIdempotency inserts the key as in progress, reserved at at, with the request hash.
// It also takes over an in-progress key with the same hash that was reserved before stale.
// reserved is true when this call holds the key. Otherwise found is the row already there.
// The insert is one statement, so two callers cannot both reserve the same key.
func (p *Postgres) ReserveIdempotency(ctx context.Context, kind, key, hash string, at, stale time.Time) (reserved bool, found IdempotencyClaim, err error) {
	for range 3 {
		var one int
		err = p.pool.QueryRow(ctx, `
INSERT INTO idempotency AS i (kind, key, payload, state, request_hash, reserved_at)
VALUES ($1, $2, '', 'in_progress', $3, $4)
ON CONFLICT (kind, key) DO UPDATE SET reserved_at = EXCLUDED.reserved_at, payload = ''
WHERE i.state = 'in_progress' AND i.request_hash = EXCLUDED.request_hash AND i.reserved_at < $5
RETURNING 1`, kind, key, hash, at, stale).Scan(&one)
		if err == nil {
			return true, IdempotencyClaim{}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, IdempotencyClaim{}, err
		}
		err = p.pool.QueryRow(ctx, `SELECT state, request_hash, payload FROM idempotency WHERE kind = $1 AND key = $2`, kind, key).
			Scan(&found.State, &found.RequestHash, &found.Payload)
		if err == nil {
			return false, found, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, IdempotencyClaim{}, err
		}
		// The holder released the key between the two statements. Try to reserve it again.
	}
	return false, IdempotencyClaim{}, fmt.Errorf("idempotency key %s was reserved and released repeatedly", kind)
}

// CompleteIdempotency marks a key reserved at at as done with its payload.
// A key another caller has taken over since is left as it is.
func (p *Postgres) CompleteIdempotency(ctx context.Context, kind, key string, at time.Time, payload string) error {
	_, err := p.pool.Exec(ctx, `
UPDATE idempotency SET state = 'done', payload = $4
WHERE kind = $1 AND key = $2 AND state = 'in_progress' AND reserved_at = $3`, kind, key, at, payload)
	return err
}

// ReleaseIdempotency deletes a key reserved at at that is still in progress, so a retry can run.
// A key another caller has taken over since is left as it is.
func (p *Postgres) ReleaseIdempotency(ctx context.Context, kind, key string, at time.Time) error {
	_, err := p.pool.Exec(ctx, `
DELETE FROM idempotency WHERE kind = $1 AND key = $2 AND state = 'in_progress' AND reserved_at = $3`, kind, key, at)
	return err
}

// JobRow is one bulk-create job stored for another process to read.
// Agent is the agent that started the job. It is empty when an operator started it
// and on a row written before the column existed.
type JobRow struct {
	ID      string
	Status  string
	Total   int
	Created int
	Error   string
	Inboxes []byte
	Agent   string
}

// SaveJob inserts a job. The inboxes value is JSON. Agent is stored once and UpdateJob does not change it.
func (p *Postgres) SaveJob(ctx context.Context, row JobRow) error {
	if row.Inboxes == nil {
		row.Inboxes = []byte("[]")
	}
	_, err := p.pool.Exec(ctx, `
INSERT INTO bulk_job (id, status, total, created, error, inboxes, agent)
VALUES ($1, $2, $3, $4, $5, $6, $7)`, row.ID, row.Status, row.Total, row.Created, row.Error, row.Inboxes, row.Agent)
	return err
}

// UpdateJob replaces the status of a job that was already saved.
func (p *Postgres) UpdateJob(ctx context.Context, row JobRow) error {
	if row.Inboxes == nil {
		row.Inboxes = []byte("[]")
	}
	_, err := p.pool.Exec(ctx, `
UPDATE bulk_job SET status = $2, created = $3, error = $4, inboxes = $5 WHERE id = $1`,
		row.ID, row.Status, row.Created, row.Error, row.Inboxes)
	return err
}

// Job reads one job. ok is false when the id is unknown.
func (p *Postgres) Job(ctx context.Context, id string) (JobRow, bool, error) {
	var row JobRow
	err := p.pool.QueryRow(ctx, `
SELECT id, status, total, created, error, inboxes, agent FROM bulk_job WHERE id = $1`, id).
		Scan(&row.ID, &row.Status, &row.Total, &row.Created, &row.Error, &row.Inboxes, &row.Agent)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobRow{}, false, nil
	}
	return row, err == nil, err
}

// SaveClient stores a hashed client secret. The plaintext secret is not stored.
func (p *Postgres) SaveClient(ctx context.Context, id, agent string, hash []byte, operator bool) error {
	_, err := p.pool.Exec(ctx, `
INSERT INTO agent_credential (id, agent, secret_hash, operator) VALUES ($1, $2, $3, $4)`, id, agent, hash, operator)
	return err
}

// Client returns one credential. ok is false when the id is unknown.
func (p *Postgres) Client(ctx context.Context, id string) (agent string, hash []byte, operator bool, ok bool, err error) {
	err = p.pool.QueryRow(ctx, `
SELECT agent, secret_hash, operator FROM agent_credential WHERE id = $1`, id).Scan(&agent, &hash, &operator)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, false, nil
	}
	return agent, hash, operator, err == nil, err
}

// AgentExists reports whether a stored credential names the agent.
func (p *Postgres) AgentExists(ctx context.Context, agent string) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_credential WHERE agent = $1)`, agent).Scan(&ok)
	return ok, err
}

// SuspendAgent records that the agent may no longer exchange a secret or use a token.
func (p *Postgres) SuspendAgent(ctx context.Context, agent string) error {
	_, err := p.pool.Exec(ctx, `
INSERT INTO agent_suspension (agent) VALUES ($1) ON CONFLICT DO NOTHING`, agent)
	return err
}

// ResumeAgent removes the suspension SuspendAgent recorded. An agent that is not suspended is unchanged.
func (p *Postgres) ResumeAgent(ctx context.Context, agent string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM agent_suspension WHERE agent = $1`, agent)
	return err
}

// AppendAudit records one operator action. The row does not store secrets.
func (p *Postgres) AppendAudit(ctx context.Context, actor, action, target string) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO audit_event (actor, action, target) VALUES ($1, $2, $3)`, actor, action, target)
	return err
}

// AuditSince returns actions with an id greater than after, in order.
// limit is the page size. The caller asks for one extra row when it needs to know that another page remains.
func (p *Postgres) AuditSince(ctx context.Context, after int64, limit int) ([]AuditRow, error) {
	rows, err := p.pool.Query(ctx, `
SELECT id, at, actor, action, target FROM audit_event WHERE id > $1 ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var row AuditRow
		if err := rows.Scan(&row.ID, &row.At, &row.Actor, &row.Action, &row.Target); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// AgentSuspended reports whether SuspendAgent has been recorded.
func (p *Postgres) AgentSuspended(ctx context.Context, agent string) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_suspension WHERE agent = $1)`, agent).Scan(&ok)
	return ok, err
}

// SaveRetention stores how many days a scope keeps mail. Zero disables deletion.
func (p *Postgres) SaveRetention(ctx context.Context, scope string, days int) error {
	_, err := p.pool.Exec(ctx, `
INSERT INTO retention_policy (scope, days) VALUES ($1, $2)
ON CONFLICT (scope) DO UPDATE SET days = EXCLUDED.days`, scope, days)
	return err
}

// Retention returns the stored days. ok is false when the scope has no row.
func (p *Postgres) Retention(ctx context.Context, scope string) (int, bool, error) {
	var days int
	err := p.pool.QueryRow(ctx, `SELECT days FROM retention_policy WHERE scope = $1`, scope).Scan(&days)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return days, err == nil, err
}

// ListRetention returns every stored retention row.
func (p *Postgres) ListRetention(ctx context.Context) ([]RetentionRow, error) {
	rows, err := p.pool.Query(ctx, `SELECT scope, days FROM retention_policy ORDER BY scope`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetentionRow
	for rows.Next() {
		var row RetentionRow
		if err := rows.Scan(&row.Scope, &row.Days); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
