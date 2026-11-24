// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/jobs"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
)

// Defaults for pruning. A done job is kept a day, a failed one a week so an operator can
// read its error, a finished webhook delivery a week, and an event 30 days.
const (
	doneJobRetention          = 24 * time.Hour
	failedJobRetention        = 7 * 24 * time.Hour
	defaultDeliveryRetainDays = 7
	defaultEventRetainDays    = 30
)

// pruneRetention reads INBAI_EVENT_RETENTION_DAYS and INBAI_DELIVERY_RETENTION_DAYS.
// Zero keeps those rows.
func pruneRetention(getenv func(string) string) (store.Retention, error) {
	keep := store.Retention{
		DoneJobs:   doneJobRetention,
		FailedJobs: failedJobRetention,
		Deliveries: defaultDeliveryRetainDays * 24 * time.Hour,
		Events:     defaultEventRetainDays * 24 * time.Hour,
	}
	if getenv == nil {
		return keep, nil
	}
	events, err := parseLimit(getenv, "INBAI_EVENT_RETENTION_DAYS", defaultEventRetainDays)
	if err != nil {
		return store.Retention{}, err
	}
	deliveries, err := parseLimit(getenv, "INBAI_DELIVERY_RETENTION_DAYS", defaultDeliveryRetainDays)
	if err != nil {
		return store.Retention{}, err
	}
	keep.Events = time.Duration(events) * 24 * time.Hour
	keep.Deliveries = time.Duration(deliveries) * 24 * time.Hour
	return keep, nil
}

// newWorker registers the background jobs. prune may be nil, and then nothing is pruned.
func newWorker(queue store.Queue, svc *service.Service, feed *events.Feed, watch mailbackend.Watcher, retentionDays int, prune store.Pruner, keep store.Retention, stderr io.Writer) *jobs.Worker {
	worker := jobs.New(queue)
	worker.SetErrorHandler(func(kind string, err error) {
		if stderr != nil {
			writef(stderr, "job %s: %s\n", kind, err)
		}
	})
	worker.Handle(jobs.KindBulkGenerate, func(ctx context.Context, raw json.RawMessage) error {
		var payload service.BulkPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return jobs.Permanent(err)
		}
		if err := svc.RunBulkJob(ctx, payload); err != nil {
			return jobs.Permanent(err)
		}
		return nil
	})
	worker.Handle(jobs.KindCursorSweep, func(ctx context.Context, _ json.RawMessage) error {
		if feed == nil || watch == nil {
			return nil
		}
		return feed.Sweep(ctx, watch)
	})
	worker.Handle(jobs.KindRetentionSweep, func(ctx context.Context, _ json.RawMessage) error {
		_, err := svc.Sweep(ctx, service.Caller{Operator: true}, retentionDays)
		return err
	})
	worker.Handle(jobs.KindPrune, func(ctx context.Context, _ json.RawMessage) error {
		if prune == nil {
			return nil
		}
		_, err := prune.Prune(ctx, time.Now(), keep)
		return err
	})
	worker.Every(jobs.KindCursorSweep, "singleton", 30*time.Second, false)
	worker.Every(jobs.KindRetentionSweep, "singleton", time.Hour, true)
	worker.Every(jobs.KindPrune, "singleton", time.Hour, true)
	return worker
}
