// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

// Package jobs runs background work from a PostgreSQL queue.
// Any server replica can claim a job. The queue is the only coordination.
package jobs
