// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Impakta-io/inbai/internal/store"
)

const (
	// KindBulkGenerate creates addresses for an asynchronous Generate request.
	KindBulkGenerate = "bulk.generate"
	// KindCursorSweep asks the mail backend for changes since each inbox cursor.
	KindCursorSweep = "cursor.sweep"
	// KindRetentionSweep deletes messages older than the configured retention period.
	KindRetentionSweep = "retention.sweep"
	// KindPrune deletes finished jobs, finished webhook deliveries, old events, and spent send counters.
	KindPrune = "store.prune"

	defaultPoll        = 500 * time.Millisecond
	defaultLease       = 2 * time.Minute
	defaultMaxAttempts = 5
	defaultBaseBackoff = 5 * time.Second
	maxBackoff         = 10 * time.Minute
)

// Handler executes one claimed job payload.
type Handler func(ctx context.Context, payload json.RawMessage) error

var errPermanent = errors.New("permanent job error")

type permanentError struct {
	err error
}

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }
func (e permanentError) Is(target error) bool {
	return target == errPermanent
}

// Permanent marks an error so the worker fails the job instead of retrying it.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

type schedule struct {
	kind      string
	uniqueKey string
	interval  time.Duration
	immediate bool
}

// Worker claims and runs jobs from a store.Queue.
type Worker struct {
	queue       store.Queue
	handlers    map[string]Handler
	schedules   []schedule
	now         func() time.Time
	poll        time.Duration
	lease       time.Duration
	maxAttempts int
	baseBackoff time.Duration
	onError     func(kind string, err error)
}

// New returns a Worker backed by queue.
func New(queue store.Queue) *Worker {
	return &Worker{
		queue:       queue,
		handlers:    map[string]Handler{},
		now:         func() time.Time { return time.Now().UTC() },
		poll:        defaultPoll,
		lease:       defaultLease,
		maxAttempts: defaultMaxAttempts,
		baseBackoff: defaultBaseBackoff,
	}
}

// Handle registers the handler for one job kind.
func (w *Worker) Handle(kind string, fn Handler) {
	w.handlers[kind] = fn
}

// Every registers a periodic singleton job. Any replica may enqueue it;
// the queue's unique key ensures only one open job exists at a time.
func (w *Worker) Every(kind, uniqueKey string, interval time.Duration, immediate bool) {
	if interval <= 0 {
		return
	}
	w.schedules = append(w.schedules, schedule{
		kind:      kind,
		uniqueKey: uniqueKey,
		interval:  interval,
		immediate: immediate,
	})
}

// SetErrorHandler records errors reported while running jobs.
func (w *Worker) SetErrorHandler(fn func(kind string, err error)) {
	w.onError = fn
}

// RunOnce claims and executes at most one due job.
// The boolean is false when no job was due.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, ok, err := w.queue.ClaimJob(ctx, w.now(), w.lease)
	if err != nil || !ok {
		return false, err
	}
	fn, registered := w.handlers[job.Kind]
	if !registered {
		return true, w.queue.FailJob(ctx, job.ID, w.now(), fmt.Sprintf("unknown job kind: %s", job.Kind))
	}
	if err := fn(ctx, job.Payload); err != nil {
		if w.onError != nil {
			w.onError(job.Kind, err)
		}
		if errors.Is(err, errPermanent) || job.Attempt >= w.maxAttempts {
			return true, w.queue.FailJob(ctx, job.ID, w.now(), err.Error())
		}
		return true, w.queue.RetryJob(ctx, job.ID, w.now().Add(w.backoff(job.Attempt)), err.Error())
	}
	return true, w.queue.AckJob(ctx, job.ID, w.now())
}

// Run schedules periodic jobs and claims queued work until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	for _, s := range w.schedules {
		go w.runSchedule(ctx, s)
	}
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for range 20 {
				ok, err := w.RunOnce(ctx)
				if err != nil || !ok {
					break
				}
			}
		}
	}
}

func (w *Worker) runSchedule(ctx context.Context, s schedule) {
	enqueue := func() {
		if _, _, err := w.queue.Enqueue(ctx, s.kind, s.uniqueKey, []byte("{}"), w.now()); err != nil && w.onError != nil {
			w.onError(s.kind, err)
		}
	}
	if s.immediate {
		enqueue()
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			enqueue()
		}
	}
}

func (w *Worker) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := w.baseBackoff
	for i := 1; i < attempt; i++ {
		if delay >= maxBackoff/2 {
			return maxBackoff
		}
		delay *= 2
	}
	if delay > maxBackoff {
		return maxBackoff
	}
	return delay
}
