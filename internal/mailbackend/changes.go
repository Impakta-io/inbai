// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import (
	"context"
	"errors"
)

// ErrChangesLost means the mail server can no longer report changes since the
// stored cursor, such as a JMAP cannotCalculateChanges or a changed IMAP
// uidvalidity. Asking again from the same cursor fails the same way.
var ErrChangesLost = errors.New("mail server cannot report changes since the stored state")

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

// InboxChecker is a Watcher that can tell whether an inbox still exists.
// False with a nil error means the server said the inbox is gone. Any doubt is an error.
type InboxChecker interface {
	InboxExists(context.Context, string) (bool, error)
}
