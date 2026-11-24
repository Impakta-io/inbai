// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"time"
)

// claimLease is how long a claimed webhook delivery, and its subscription's in-flight
// mark, stay leased. It is three times the 10 second request timeout. A process that
// stops mid-delivery leaves the lease to expire, and the delivery is claimed again.
const claimLease = 30 * time.Second

// Retention says how long Prune keeps finished rows. A zero duration keeps those rows.
type Retention struct {
	// DoneJobs keeps job_queue rows that finished.
	DoneJobs time.Duration
	// FailedJobs keeps job_queue rows that failed for good.
	FailedJobs time.Duration
	// Deliveries keeps webhook deliveries that were delivered or failed for good,
	// counted from when that happened.
	Deliveries time.Duration
	// Events keeps mail_event rows, counted from when the event was logged. An event
	// that still has a webhook delivery row is kept until that row is pruned.
	Events time.Duration
}

// Pruned counts the rows one Prune deleted.
type Pruned struct {
	Jobs       int64
	Deliveries int64
	Events     int64
	Quotas     int64
}

// Pruner deletes rows that are no longer needed.
type Pruner interface {
	Prune(ctx context.Context, now time.Time, keep Retention) (Pruned, error)
}

const (
	// pruneBatch is the most rows one DELETE removes, so no statement holds locks for long.
	pruneBatch = 1000
	// pruneBatches caps the statements per table in one Prune. The next run continues.
	pruneBatches = 100
	// quotaGrace keeps a counter this long after its window ends, so a replica whose
	// clock runs a little behind still finds the count it is adding to.
	quotaGrace = time.Hour
)

// Prune deletes, in batches: finished and failed jobs, finished webhook deliveries,
// events older than keep.Events that no delivery row names, and send counters whose
// day or minute has passed. Counter keys end in a UTC day (2006-01-02) or minute
// (2006-01-02T15:04) after a newline, as internal/service writes them. A counter
// with any other key is kept.
func (p *Postgres) Prune(ctx context.Context, now time.Time, keep Retention) (Pruned, error) {
	var out Pruned
	var err error
	now = now.UTC()
	if keep.DoneJobs > 0 {
		var n int64
		if n, err = p.pruneBatches(ctx, `
DELETE FROM job_queue WHERE id IN (SELECT id FROM job_queue WHERE done_at < $1 LIMIT $2)`, now.Add(-keep.DoneJobs)); err != nil {
			return out, err
		}
		out.Jobs += n
	}
	if keep.FailedJobs > 0 {
		var n int64
		if n, err = p.pruneBatches(ctx, `
DELETE FROM job_queue WHERE id IN (SELECT id FROM job_queue WHERE failed_at < $1 LIMIT $2)`, now.Add(-keep.FailedJobs)); err != nil {
			return out, err
		}
		out.Jobs += n
	}
	if keep.Deliveries > 0 {
		if out.Deliveries, err = p.pruneBatches(ctx, `
DELETE FROM webhook_delivery WHERE id IN (
    SELECT id FROM webhook_delivery WHERE done_at < $1 OR failed_at < $1 LIMIT $2)`, now.Add(-keep.Deliveries)); err != nil {
			return out, err
		}
	}
	if keep.Events > 0 {
		// An event a delivery still names stays: a pending delivery is sent from it, and a
		// backup must not hold a delivery without its event.
		if out.Events, err = p.pruneBatches(ctx, `
DELETE FROM mail_event WHERE id IN (
    SELECT event.id FROM mail_event AS event
    WHERE event.at < $1
      AND NOT EXISTS (SELECT 1 FROM webhook_delivery AS delivery WHERE delivery.event_id = event.id)
    ORDER BY event.id LIMIT $2)`, now.Add(-keep.Events)); err != nil {
			return out, err
		}
	}
	cutoff := now.Add(-quotaGrace)
	out.Quotas, err = p.pruneBatches(ctx, `
DELETE FROM quota_counter WHERE bucket IN (
    SELECT bucket FROM quota_counter
    WHERE (bucket ~ '\n\d{4}-\d{2}-\d{2}$' AND right(bucket, 10) COLLATE "C" < $1)
       OR (bucket ~ '\n\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$' AND right(bucket, 16) COLLATE "C" < $2)
    LIMIT $3)`, cutoff.Format(time.DateOnly), cutoff.Format("2006-01-02T15:04"))
	return out, err
}

// pruneBatches runs a DELETE whose last parameter is the batch size until a batch
// deletes fewer rows than that, the batch cap is reached, or ctx ends.
func (p *Postgres) pruneBatches(ctx context.Context, query string, args ...any) (int64, error) {
	args = append(args, pruneBatch)
	var total int64
	for range pruneBatches {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		tag, err := p.pool.Exec(ctx, query, args...)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < pruneBatch {
			break
		}
	}
	return total, nil
}

// Prune deletes finished deliveries and old events, with the same rules as Postgres.
// The in-memory log has no job queue or send counters.
func (m *Memory) Prune(_ context.Context, now time.Time, keep Retention) (Pruned, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	var out Pruned
	if keep.Deliveries > 0 {
		cutoff := now.Add(-keep.Deliveries)
		kept := m.deliveries[:0]
		for _, item := range m.deliveries {
			if (item.done || item.failed) && item.finished.Before(cutoff) {
				out.Deliveries++
				continue
			}
			kept = append(kept, item)
		}
		m.deliveries = kept
	}
	if keep.Events > 0 {
		cutoff := now.Add(-keep.Events)
		named := map[int64]bool{}
		for _, item := range m.deliveries {
			named[item.eventID] = true
		}
		kept := m.events[:0]
		for _, event := range m.events {
			if event.at.Before(cutoff) && !named[event.ID] {
				out.Events++
				continue
			}
			kept = append(kept, event)
		}
		m.events = kept
	}
	return out, nil
}

// Prune deletes finished and failed jobs, with the same rules as Postgres.
func (m *MemoryQueue) Prune(_ context.Context, now time.Time, keep Retention) (Pruned, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out Pruned
	kept := m.jobs[:0]
	for _, job := range m.jobs {
		at := m.finished[job.ID]
		if (job.Done && keep.DoneJobs > 0 && at.Before(now.Add(-keep.DoneJobs))) ||
			(job.Failed && keep.FailedJobs > 0 && at.Before(now.Add(-keep.FailedJobs))) {
			delete(m.finished, job.ID)
			out.Jobs++
			continue
		}
		kept = append(kept, job)
	}
	m.jobs = kept
	return out, nil
}
