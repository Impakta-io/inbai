// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

func TestSweepDeletesMailOlderThanRetention(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	mail := &fakeMail{messages: []mailbackend.Message{
		{ID: "old", ReceivedAt: now.AddDate(0, 0, -31)},
		{ID: "new", ReceivedAt: now.AddDate(0, 0, -1)},
		{ID: "undated"},
	}}
	svc := New(mail)
	svc.now = func() time.Time { return now }
	if _, err := svc.Sweep(context.Background(), Caller{Agent: "ada"}, 30); !errors.Is(err, ErrForbidden) || mail.dir {
		t.Fatalf("agent sweep err %v listed %v", err, mail.dir)
	}
	if err := svc.SetRetention(Caller{Agent: "ada"}, "", 7); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent set %v", err)
	}
	n, err := svc.Sweep(context.Background(), Caller{Operator: true}, 30)
	if err != nil || n != 1 || len(mail.removed) != 1 || mail.removed[0] != "old" {
		t.Fatalf("deleted %d removed %v err %v", n, mail.removed, err)
	}
	if err := svc.SetRetention(Caller{Operator: true}, "alice-id", 0); err != nil {
		t.Fatal(err)
	}
	mail.removed = nil
	n, err = svc.Sweep(context.Background(), Caller{Operator: true}, 30)
	if err != nil || n != 0 || len(mail.removed) != 0 {
		t.Fatalf("override deleted %d removed %v err %v", n, mail.removed, err)
	}
	days, set, err := svc.RetentionDays(Caller{Operator: true}, "")
	if err != nil || set || days != 0 {
		t.Fatalf("server days %d set %v err %v", days, set, err)
	}
}
