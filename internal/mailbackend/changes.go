// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import "context"

// Delta is a JMAP change since a stored state cursor.
type Delta struct {
	State     string
	Created   []string
	Destroyed []string
	More      bool
}

// Watcher reports mailbox changes. The event cursor is the record. A webhook is only a hint to ask.
type Watcher interface {
	MailState(context.Context, string) (string, error)
	MailChanges(context.Context, string, string) (Delta, error)
}
