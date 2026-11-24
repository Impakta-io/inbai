// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// QueueJob is one background work item stored in job_queue.
type QueueJob struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	UniqueKey string          `json:"uniqueKey,omitempty"`
	Attempt   int             `json:"attempt"`
	Next      time.Time       `json:"next"`
	Done      bool            `json:"done"`
	Failed    bool            `json:"failed"`
	LastError string          `json:"lastError,omitempty"`
}

// Queue is the durable background job queue claimed by workers across replicas.
type Queue interface {
	Enqueue(ctx context.Context, kind, uniqueKey string, payload []byte, runAt time.Time) (int64, bool, error)
	ClaimJob(ctx context.Context, now time.Time, lease time.Duration) (QueueJob, bool, error)
	AckJob(ctx context.Context, id int64, now time.Time) error
	RetryJob(ctx context.Context, id int64, next time.Time, lastErr string) error
	FailJob(ctx context.Context, id int64, now time.Time, lastErr string) error
}

// Enqueue adds a job to job_queue. When uniqueKey is non-empty and an open job
// with the same kind and key already exists, Enqueue returns (0, false, nil).
func (p *Postgres) Enqueue(ctx context.Context, kind, uniqueKey string, payload []byte, runAt time.Time) (int64, bool, error) {
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	if runAt.IsZero() {
		runAt = time.Now().UTC()
	}
	var id int64
	if uniqueKey != "" {
		err := p.pool.QueryRow(ctx, `
INSERT INTO job_queue (kind, unique_key, payload, next_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (kind, unique_key) WHERE unique_key <> '' AND done_at IS NULL AND failed_at IS NULL DO NOTHING
RETURNING id`, kind, uniqueKey, payload, runAt).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return id, err == nil, err
	}
	err := p.pool.QueryRow(ctx, `
INSERT INTO job_queue (kind, unique_key, payload, next_at)
VALUES ($1, '', $2, $3)
RETURNING id`, kind, payload, runAt).Scan(&id)
	return id, err == nil, err
}

// ClaimJob claims the next due job using FOR UPDATE SKIP LOCKED and extends its lease.
func (p *Postgres) ClaimJob(ctx context.Context, now time.Time, lease time.Duration) (QueueJob, bool, error) {
	if lease <= 0 {
		lease = 30 * time.Second
	}
	var job QueueJob
	err := p.pool.QueryRow(ctx, `
WITH claimed AS (
    SELECT id FROM job_queue
    WHERE done_at IS NULL AND failed_at IS NULL AND next_at <= $1
    ORDER BY next_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE job_queue AS job
SET attempt = job.attempt + 1, next_at = $2
FROM claimed
WHERE job.id = claimed.id
RETURNING job.id, job.kind, job.payload, job.unique_key, job.attempt, job.next_at, job.last_error`,
		now, now.Add(lease)).Scan(
		&job.ID, &job.Kind, &job.Payload, &job.UniqueKey, &job.Attempt, &job.Next, &job.LastError,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return QueueJob{}, false, nil
	}
	if err != nil {
		return QueueJob{}, false, err
	}
	return job, true, nil
}

// AckJob marks a claimed job complete.
func (p *Postgres) AckJob(ctx context.Context, id int64, now time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE job_queue SET done_at = $2, last_error = '' WHERE id = $1`, id, now)
	return err
}

// RetryJob schedules a claimed job to run again at next.
func (p *Postgres) RetryJob(ctx context.Context, id int64, next time.Time, lastErr string) error {
	_, err := p.pool.Exec(ctx, `UPDATE job_queue SET next_at = $2, last_error = $3 WHERE id = $1`, id, next, lastErr)
	return err
}

// FailJob marks a claimed job permanently failed.
func (p *Postgres) FailJob(ctx context.Context, id int64, now time.Time, lastErr string) error {
	_, err := p.pool.Exec(ctx, `UPDATE job_queue SET failed_at = $2, last_error = $3 WHERE id = $1`, id, now, lastErr)
	return err
}

func readQueue(ctx context.Context, tx pgx.Tx, snap *Snapshot) error {
	snap.Queue = []QueueJob{}
	rows, err := tx.Query(ctx, `
SELECT id, kind, payload, unique_key, attempt, next_at, done_at IS NOT NULL, failed_at IS NOT NULL, last_error
FROM job_queue ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row QueueJob
		if err := rows.Scan(&row.ID, &row.Kind, &row.Payload, &row.UniqueKey, &row.Attempt, &row.Next, &row.Done, &row.Failed, &row.LastError); err != nil {
			return err
		}
		snap.Queue = append(snap.Queue, row)
	}
	return rows.Err()
}

func writeQueue(ctx context.Context, tx pgx.Tx, snap Snapshot) error {
	if snap.Queue == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `TRUNCATE job_queue`); err != nil {
		return err
	}
	var max int64
	for _, row := range snap.Queue {
		payload := row.Payload
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		var done, failed any
		if row.Done {
			done = row.Next
		}
		if row.Failed {
			failed = row.Next
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO job_queue (id, kind, payload, unique_key, attempt, next_at, done_at, failed_at, last_error)
OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			row.ID, row.Kind, payload, row.UniqueKey, row.Attempt, row.Next, done, failed, row.LastError); err != nil {
			return err
		}
		if row.ID > max {
			max = row.ID
		}
	}
	return setIdentity(ctx, tx, "job_queue", "id", max)
}

// MemoryQueue is an in-process Queue for tests.
type MemoryQueue struct {
	mu   sync.Mutex
	next int64
	jobs []*QueueJob
	// finished is when each done or failed job ended, for Prune.
	finished map[int64]time.Time
}

func (m *MemoryQueue) end(id int64, now time.Time) {
	if m.finished == nil {
		m.finished = map[int64]time.Time{}
	}
	m.finished[id] = now
}

func (m *MemoryQueue) Enqueue(_ context.Context, kind, uniqueKey string, payload []byte, runAt time.Time) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if uniqueKey != "" {
		for _, job := range m.jobs {
			if !job.Done && !job.Failed && job.Kind == kind && job.UniqueKey == uniqueKey {
				return 0, false, nil
			}
		}
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	if runAt.IsZero() {
		runAt = time.Now().UTC()
	}
	m.next++
	cp := append([]byte(nil), payload...)
	m.jobs = append(m.jobs, &QueueJob{
		ID:        m.next,
		Kind:      kind,
		Payload:   cp,
		UniqueKey: uniqueKey,
		Next:      runAt,
	})
	return m.next, true, nil
}

func (m *MemoryQueue) ClaimJob(_ context.Context, now time.Time, lease time.Duration) (QueueJob, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lease <= 0 {
		lease = 30 * time.Second
	}
	sort.Slice(m.jobs, func(i, j int) bool {
		if m.jobs[i].Next.Equal(m.jobs[j].Next) {
			return m.jobs[i].ID < m.jobs[j].ID
		}
		return m.jobs[i].Next.Before(m.jobs[j].Next)
	})
	for _, job := range m.jobs {
		if job.Done || job.Failed || job.Next.After(now) {
			continue
		}
		job.Attempt++
		job.Next = now.Add(lease)
		cp := *job
		cp.Payload = append([]byte(nil), job.Payload...)
		return cp, true, nil
	}
	return QueueJob{}, false, nil
}

func (m *MemoryQueue) AckJob(_ context.Context, id int64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if job.ID == id {
			job.Done = true
			m.end(id, now)
			job.LastError = ""
			return nil
		}
	}
	return nil
}

func (m *MemoryQueue) RetryJob(_ context.Context, id int64, next time.Time, lastErr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if job.ID == id {
			job.Next = next
			job.LastError = lastErr
			return nil
		}
	}
	return nil
}

func (m *MemoryQueue) FailJob(_ context.Context, id int64, now time.Time, lastErr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if job.ID == id {
			job.Failed = true
			m.end(id, now)
			job.LastError = lastErr
			return nil
		}
	}
	return nil
}

// Jobs returns a snapshot copy of all jobs in the memory queue.
func (m *MemoryQueue) Jobs() []QueueJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]QueueJob, len(m.jobs))
	for i, job := range m.jobs {
		cp := *job
		cp.Payload = append([]byte(nil), job.Payload...)
		out[i] = cp
	}
	return out
}
