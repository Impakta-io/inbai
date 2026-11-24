// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestResumeLetsASuspendedAgentBackIn(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		dir := auth.NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
		checkResume(t, New(&fakeMail{}), dir, "ada")
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("INBAI_TEST_DATABASE")
		if dsn == "" {
			t.Skip("INBAI_TEST_DATABASE is not set")
		}
		db, err := store.Open(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(db.Close)
		svc := New(&fakeMail{})
		svc.SetDB(db)
		dir := auth.NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
		dir.SetDB(db)
		// A name unique to this run, so rows left by an earlier run do not match.
		checkResume(t, svc, dir, "resume-ada-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	})
}

// checkResume suspends and resumes agent through svc, with the directory that verifies its tokens.
func checkResume(t *testing.T, svc *Service, dir *auth.Directory, agent string) {
	t.Helper()
	operator := Caller{Operator: true}
	id, secret, err := svc.ProvisionAgent(operator, dir, agent)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := dir.Exchange(id, secret)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.ResumeAgent(operator, dir, agent); err != nil {
		t.Fatalf("resume when not suspended %v", err)
	}
	if err := svc.SuspendAgent(operator, dir, agent); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dir.Verify(token); !errors.Is(err, auth.ErrRejected) {
		t.Fatalf("suspended verify %v", err)
	}
	if _, _, err := dir.Exchange(id, secret); !errors.Is(err, auth.ErrRejected) {
		t.Fatalf("suspended exchange %v", err)
	}
	if err := svc.ResumeAgent(Caller{Agent: agent}, dir, agent); !errors.Is(err, ErrForbidden) {
		t.Fatalf("agent resumed itself: %v", err)
	}
	if _, _, err := dir.Verify(token); !errors.Is(err, auth.ErrRejected) {
		t.Fatalf("verify after a refused resume %v", err)
	}
	if err := svc.ResumeAgent(operator, dir, "nobody-"+agent); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown agent %v", err)
	}

	if err := svc.ResumeAgent(operator, dir, agent); err != nil {
		t.Fatal(err)
	}
	if subject, _, err := dir.Verify(token); err != nil || subject != agent {
		t.Fatalf("token issued before the suspension: subject %q err %v", subject, err)
	}
	if _, _, err := dir.Exchange(id, secret); err != nil {
		t.Fatalf("exchange after resume %v", err)
	}
	if got := auditFor(t, svc, agent); got != "agent.create agent.resume agent.suspend agent.resume" {
		t.Fatalf("audit %q", got)
	}
}

// auditFor lists the actions whose target is agent, reading every page.
func auditFor(t *testing.T, svc *Service, agent string) string {
	t.Helper()
	var actions []string
	var after int64
	for {
		rows, next, err := svc.Audit(context.Background(), Caller{Operator: true}, after, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Target == agent {
				actions = append(actions, row.Action)
			}
		}
		if next == 0 {
			return strings.Join(actions, " ")
		}
		after = next
	}
}
