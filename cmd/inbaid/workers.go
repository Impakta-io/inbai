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

func newWorker(queue store.Queue, svc *service.Service, feed *events.Feed, watch mailbackend.Watcher, retentionDays int, stderr io.Writer) *jobs.Worker {
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
	worker.Every(jobs.KindCursorSweep, "singleton", 30*time.Second, false)
	worker.Every(jobs.KindRetentionSweep, "singleton", time.Hour, true)
	return worker
}
