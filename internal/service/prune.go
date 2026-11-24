// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"strings"
	"time"
)

const (
	// quotaGrace keeps a send counter this long after its window ends. It matches the
	// grace internal/store uses when it prunes counters in PostgreSQL.
	quotaGrace = time.Hour
	// idempotencyMemoryRetention is how long a service without a database remembers an
	// Idempotency-Key. PostgreSQL keeps finished keys until the inbox is deleted. Memory
	// does not, so a key repeated after this long runs again.
	idempotencyMemoryRetention = 24 * time.Hour
)

// PruneMemory drops in-process state that can no longer matter, for a service without a
// database. A service with a database does nothing: store.Prune does this work there.
//
// It removes send counters whose day or minute ended more than an hour ago, and
// idempotency keys reserved more than 24 hours ago. A key that is still in progress is
// older than idempotencyTakeover long before then, so a live request never loses its key.
// It returns how many counters and keys it removed.
func (s *Service) PruneMemory(now time.Time) (counters, keys int) {
	if s.db != nil {
		return 0, 0
	}
	now = now.UTC()
	cutoff := now.Add(-quotaGrace)
	day, minute := cutoff.Format(time.DateOnly), cutoff.Format(minuteLayout)
	keyCutoff := now.Add(-idempotencyMemoryRetention)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.sent {
		if bucketEnded(key, day, minute) {
			delete(s.sent, key)
			counters++
		}
	}
	for key, entry := range s.idem {
		if entry.at.Before(keyCutoff) {
			delete(s.idem, key)
			keys++
		}
	}
	return counters, keys
}

const minuteLayout = "2006-01-02T15:04"

// bucketEnded reports whether a counter key, which ends in a UTC day (2006-01-02) or
// minute (2006-01-02T15:04) after a newline, names a window before day or minute.
// A key with any other ending is kept, as in store.Prune.
func bucketEnded(key, day, minute string) bool {
	i := strings.LastIndexByte(key, '\n')
	if i < 0 {
		return false
	}
	window := key[i+1:]
	if _, err := time.Parse(time.DateOnly, window); err == nil {
		return window < day
	}
	if _, err := time.Parse(minuteLayout, window); err == nil {
		return window < minute
	}
	return false
}
