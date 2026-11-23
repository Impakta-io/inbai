// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

// Package store is the PostgreSQL access layer for inbAI state.
// Message bodies stay in the mail backend. This package holds the event log.
// Identities, grants, and jobs move here when they leave the server process.
// Every row stores workspace_id. This server uses one workspace.
package store
