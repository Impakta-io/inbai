// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/store"
)

// deleteEnv is a service with a feed, on the in-memory stores or on PostgreSQL.
type deleteEnv struct {
	svc  *Service
	mail *checkedMail
	log  store.Log
	feed *events.Feed
	// name makes agent and inbox names unique, so rows left by an earlier run do not match.
	name func(string) string
	// pending counts the webhook deliveries not yet sent, for every inbox.
	pending func(t *testing.T) int
	// pool reaches the database directly. It is nil in memory.
	pool *pgxpool.Pool
	// warnings holds what the service logged.
	warnings *syncBuffer
}

// checkedMail is fakeMail with an answer to InboxExists.
type checkedMail struct {
	*fakeMail
	exists   bool
	checkErr error
}

func (c *checkedMail) InboxExists(context.Context, string) (bool, error) {
	return c.exists, c.checkErr
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newDeleteEnv(log store.Log, db *store.Postgres) deleteEnv {
	mail := &checkedMail{fakeMail: &fakeMail{}, exists: true}
	svc := New(mail)
	if db != nil {
		svc.SetDB(db)
	}
	feed := events.New(log)
	feed.SetAllowPrivate(true)
	svc.SetFeed(feed)
	warnings := &syncBuffer{}
	svc.notices = slog.New(slog.NewTextHandler(warnings, nil))
	return deleteEnv{svc: svc, mail: mail, log: log, feed: feed, warnings: warnings, name: func(s string) string { return s }}
}

func memoryDeleteEnv(log store.Log) deleteEnv {
	env := newDeleteEnv(log, nil)
	env.pending = func(t *testing.T) int {
		t.Helper()
		// Claim takes one delivery per webhook at a time. Each webhook here has one.
		n := 0
		for {
			_, ok, err := log.Claim(context.Background(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				return n
			}
			n++
		}
	}
	return env
}

// postgresDeleteEnv opens the test database. params are added to the DSN of the
// database the service uses.
func postgresDeleteEnv(t *testing.T, params string) deleteEnv {
	t.Helper()
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	if dsn == "" {
		t.Skip("INBAI_TEST_DATABASE is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`)
		conn.Release()
	})
	if params != "" {
		if strings.Contains(dsn, "://") && !strings.Contains(dsn, "?") {
			dsn += "?" + params
		} else if strings.Contains(dsn, "://") {
			dsn += "&" + params
		} else {
			dsn += " " + strings.ReplaceAll(params, "&", " ")
		}
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	// pending counts every delivery, so clear what other tests queued.
	if _, err := pool.Exec(ctx, `TRUNCATE webhook_delivery, webhook_subscription`); err != nil {
		t.Fatal(err)
	}
	env := newDeleteEnv(db, db)
	env.pool = pool
	suffix := "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	env.name = func(s string) string { return s + suffix }
	env.pending = func(t *testing.T) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM webhook_delivery WHERE done_at IS NULL AND failed_at IS NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return env
}

// fill gives an inbox two agents' grants, an allowlist, a retention policy, a cursor,
// an agent and an operator webhook, and one event, which queues a delivery to each webhook.
func (e deleteEnv) fill(t *testing.T, inbox string) {
	t.Helper()
	ctx := context.Background()
	for _, agent := range []string{e.name("ada"), e.name("cy")} {
		for _, perm := range []Permission{PermRead, PermManage} {
			if err := e.svc.Grant(opCaller, agent, inbox, perm); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := e.svc.SetAllowlist(ctx, opCaller, inbox, []string{"example.org"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetRetention(opCaller, inbox, 7); err != nil {
		t.Fatal(err)
	}
	if err := e.feed.Remember(ctx, inbox, inbox+"@example.com"); err != nil {
		t.Fatal(err)
	}
	for _, caller := range []Caller{{Agent: e.name("ada")}, opCaller} {
		if _, _, err := e.svc.SubscribeWebhook(ctx, caller, inbox, "http://127.0.0.1:9/"+inbox); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.log.Put(ctx, inbox, "", "s1", []store.Record{{MessageID: "m1", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
}

func (e deleteEnv) create(t *testing.T, local, id string) string {
	t.Helper()
	e.mail.created = id
	got, err := e.svc.CreateInbox(context.Background(), opCaller, mailbackend.InboxSpec{Local: local}, "key-1")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (e deleteEnv) can(t *testing.T, agent, inbox string, perm Permission) bool {
	t.Helper()
	ok, err := e.svc.allowed(context.Background(), Caller{Agent: agent}, inbox, perm)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// kept reports what the service and the feed still hold for an inbox.
type kept struct {
	grants, allowlist, retention, cursor bool
	webhooks, events                     int
}

func (e deleteEnv) kept(t *testing.T, inbox string) kept {
	t.Helper()
	ctx := context.Background()
	var out kept
	for _, agent := range []string{e.name("ada"), e.name("cy")} {
		for _, perm := range []Permission{PermRead, PermManage} {
			out.grants = out.grants || e.can(t, agent, inbox, perm)
		}
	}
	var err error
	if _, out.allowlist, err = e.svc.Allowlist(ctx, opCaller, inbox); err != nil {
		t.Fatal(err)
	}
	if _, out.retention, err = e.svc.RetentionDays(opCaller, inbox); err != nil {
		t.Fatal(err)
	}
	_, out.cursor, err = e.log.InboxByAddress(ctx, inbox+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	subs, err := e.log.Subscriptions(ctx, inbox)
	if err != nil {
		t.Fatal(err)
	}
	out.webhooks = len(subs)
	records, err := e.feed.Since(ctx, inbox, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	out.events = len(records)
	return out
}

var (
	filled = kept{grants: true, allowlist: true, retention: true, cursor: true, webhooks: 2, events: 1}
	gone   = kept{events: 1}
)

func (e deleteEnv) audits(t *testing.T, action, target string) int {
	t.Helper()
	n := 0
	var after int64
	for {
		rows, next, err := e.svc.Audit(context.Background(), opCaller, after, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Action == action && row.Target == target {
				n++
			}
		}
		if next == 0 {
			return n
		}
		after = next
	}
}

func TestDeleteInboxForgetsTheInboxInMemory(t *testing.T) {
	checkDeleteForgetsTheInbox(t, memoryDeleteEnv(&store.Memory{}))
}

func TestDeleteInboxForgetsTheInboxInPostgres(t *testing.T) {
	checkDeleteForgetsTheInbox(t, postgresDeleteEnv(t, ""))
}

// checkDeleteForgetsTheInbox deletes one of two filled inboxes, then creates a new inbox
// that the mail server gives the same id, as an IMAP account key can be reused.
func checkDeleteForgetsTheInbox(t *testing.T, env deleteEnv) {
	ctx := context.Background()
	alice, bob := env.name("alice-id"), env.name("bob-id")
	if got := env.create(t, env.name("alice"), alice); got != alice {
		t.Fatalf("created %q", got)
	}
	if got := env.create(t, env.name("bob"), bob); got != bob {
		t.Fatalf("created %q", got)
	}
	env.fill(t, alice)
	env.fill(t, bob)
	if got := env.kept(t, alice); got != filled {
		t.Fatalf("filled %+v", got)
	}
	if err := env.svc.DeleteInbox(ctx, opCaller, alice); err != nil {
		t.Fatal(err)
	}
	if got := env.kept(t, alice); got != gone {
		t.Fatalf("deleted inbox kept %+v, want only its events", got)
	}
	if got := env.kept(t, bob); got != filled {
		t.Fatalf("other inbox kept %+v", got)
	}
	if got := env.pending(t); got != 2 {
		t.Fatalf("pending deliveries %d, want only the other inbox's 2", got)
	}
	if env.audits(t, "inbox.delete", alice) != 1 || env.audits(t, "inbox.create", alice) != 1 {
		t.Fatal("the audit rows were not kept")
	}
	if warnings := env.warnings.String(); warnings != "" {
		t.Fatalf("warnings %s", warnings)
	}

	// The other inbox's create key still answers without a backend call.
	creates := env.mail.creates
	if got := env.create(t, env.name("bob"), "unused"); got != bob || env.mail.creates != creates {
		t.Fatalf("other create key answered %q, creates %d", got, env.mail.creates-creates)
	}
	// The deleted inbox's create key is gone, so a repeat creates again. The mail server
	// gives the new account the old id, and it starts with nothing from the old inbox.
	if got := env.create(t, env.name("alice"), alice); got != alice || env.mail.creates != creates+1 {
		t.Fatalf("repeat create answered %q, creates %d", got, env.mail.creates-creates)
	}
	if got := env.kept(t, alice); got != gone {
		t.Fatalf("reused id starts with %+v", got)
	}
	if _, _, err := env.svc.List(ctx, Caller{Agent: env.name("ada")}, alice, mailbackend.Page{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("list on the reused id: %v", err)
	}
}

// flakyForget fails Forget while fail is set.
type flakyForget struct {
	store.Log
	fail bool
}

func (f *flakyForget) Forget(ctx context.Context, inbox string) error {
	if f.fail {
		return errors.New("database down")
	}
	return f.Log.Forget(ctx, inbox)
}

func TestDeleteInboxSucceedsAndWarnsWhenTheCleanupFailsInMemory(t *testing.T) {
	ctx := context.Background()
	log := &flakyForget{Log: &store.Memory{}, fail: true}
	env := memoryDeleteEnv(log)
	env.fill(t, "alice-id")
	if err := env.svc.DeleteInbox(ctx, opCaller, "alice-id"); err != nil {
		t.Fatalf("delete %v", err)
	}
	if got := env.kept(t, "alice-id"); !got.cursor || got.grants || got.webhooks != 0 {
		t.Fatalf("after the failed cleanup %+v", got)
	}
	checkWarned(t, env, "alice-id", "database down")
	if env.audits(t, "inbox.delete", "alice-id") != 1 {
		t.Fatal("the delete was not audited")
	}
	// The account is gone now. A repeat delete fails at the mail server, which reports
	// the account missing, and finishes the cleanup.
	log.fail = false
	env.mail.dropErr = errors.New("account alice-id was not deleted")
	env.mail.exists = false
	if err := env.svc.DeleteInbox(ctx, opCaller, "alice-id"); !errors.Is(err, env.mail.dropErr) {
		t.Fatalf("repeat delete %v", err)
	}
	if got := env.kept(t, "alice-id"); got != gone {
		t.Fatalf("after the retry %+v", got)
	}
	if env.audits(t, "inbox.delete", "alice-id") != 1 {
		t.Fatal("the repeat was audited as a delete")
	}
}

func TestDeleteInboxSucceedsAndWarnsWhenTheCleanupFailsInPostgres(t *testing.T) {
	env := postgresDeleteEnv(t, "lock_timeout=200")
	ctx := context.Background()
	alice := env.name("alice-id")
	env.fill(t, alice)
	// store.ForgetInbox deletes from sender_allowlist after the webhooks, cursor, and grants.
	lock, err := env.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `LOCK TABLE sender_allowlist IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	err = env.svc.DeleteInbox(ctx, opCaller, alice)
	_ = lock.Rollback(ctx)
	if err != nil {
		t.Fatalf("delete %v", err)
	}
	// One transaction: nothing was removed.
	if got := env.kept(t, alice); got != filled {
		t.Fatalf("after the failed cleanup %+v", got)
	}
	checkWarned(t, env, alice, "lock timeout")
	if env.audits(t, "inbox.delete", alice) != 1 {
		t.Fatal("the delete was not audited")
	}
	env.mail.dropErr = errors.New("account was not deleted")
	env.mail.exists = false
	if err := env.svc.DeleteInbox(ctx, opCaller, alice); !errors.Is(err, env.mail.dropErr) {
		t.Fatalf("repeat delete %v", err)
	}
	if got := env.kept(t, alice); got != gone {
		t.Fatalf("after the retry %+v", got)
	}
	if got := env.pending(t); got != 0 {
		t.Fatalf("pending deliveries %d", got)
	}
}

func checkWarned(t *testing.T, env deleteEnv, inbox, cause string) {
	t.Helper()
	warnings := env.warnings.String()
	if !strings.Contains(warnings, "level=WARN") || !strings.Contains(warnings, "inbox="+inbox) || !strings.Contains(warnings, cause) {
		t.Fatalf("warnings %q", warnings)
	}
}

// A failed account delete removes nothing unless the mail server says the account is gone.
func TestDeleteInboxKeepsEverythingWhileTheAccountMayExist(t *testing.T) {
	ctx := context.Background()
	env := memoryDeleteEnv(&store.Memory{})
	env.fill(t, "alice-id")
	env.mail.dropErr = errors.New("backend down")
	for _, answer := range []struct {
		exists bool
		err    error
	}{{true, nil}, {false, errors.New("lookup failed")}} {
		env.mail.exists, env.mail.checkErr = answer.exists, answer.err
		if err := env.svc.DeleteInbox(ctx, opCaller, "alice-id"); !errors.Is(err, env.mail.dropErr) {
			t.Fatalf("delete %v", err)
		}
		if got := env.kept(t, "alice-id"); got != filled {
			t.Fatalf("exists %v check %v: kept %+v", answer.exists, answer.err, got)
		}
	}
	if env.audits(t, "inbox.delete", "alice-id") != 0 {
		t.Fatal("a failed delete was audited")
	}
}

// * is the scope of inbox:provision grants. Deleting it would drop every one of them.
func TestDeleteInboxRefusesTheProvisionScope(t *testing.T) {
	env := memoryDeleteEnv(&store.Memory{})
	if err := env.svc.Grant(opCaller, "ada", provisionScope, PermProvision); err != nil {
		t.Fatal(err)
	}
	env.mail.exists = false
	if err := env.svc.DeleteInbox(context.Background(), opCaller, provisionScope); !errors.Is(err, ErrInvalid) || env.mail.dir {
		t.Fatalf("err %v called %v", err, env.mail.dir)
	}
	if !env.can(t, "ada", provisionScope, PermProvision) {
		t.Fatal("the provision grant was removed")
	}
}
