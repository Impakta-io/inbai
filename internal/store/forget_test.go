// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// seedInbox stores one of every row kept for an inbox: a cursor with an address, an
// agent and an operator webhook with a pending and a finished delivery, grants for
// two agents, an allowlist, a retention policy, a create key, a send key, an event,
// and an audit row.
func seedInbox(t *testing.T, db *Postgres, inbox string) {
	t.Helper()
	ctx := context.Background()
	for _, sub := range []Subscription{
		{ID: inbox + "-hook", Agent: "ada", Inbox: inbox, URL: "https://agent.example/" + inbox, Secret: "s"},
		{ID: inbox + "-op", Agent: "operator", Inbox: inbox, URL: "https://ops.example/" + inbox, Secret: "s", Operator: true},
	} {
		if err := db.Subscribe(ctx, sub, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Put(ctx, inbox, inbox+"@example.com", "s1", []Record{{MessageID: "m1", Kind: "created"}, {MessageID: "m2", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	delivery, ok, err := db.Claim(ctx, time.Now().Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("claim ok %v err %v", ok, err)
	}
	if err := db.Ack(ctx, delivery.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"ada", "bob"} {
		for _, perm := range []string{"read", "send"} {
			if err := db.SaveGrant(ctx, agent, inbox, perm); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.SaveAllowlist(ctx, inbox, []string{"example.org"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveRetention(ctx, "inbox:"+inbox, 7); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveIdempotency(ctx, "created", "operator\n"+inbox+"-local\nkey", inbox); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveIdempotency(ctx, "sent", "operator\n"+inbox+"\nkey", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendAudit(ctx, "operator", "inbox.create", inbox); err != nil {
		t.Fatal(err)
	}
}

// inboxRows counts the rows each table holds for an inbox.
func inboxRows(t *testing.T, db *Postgres, inbox string) map[string]int {
	t.Helper()
	ctx := context.Background()
	out := map[string]int{}
	for name, query := range map[string]string{
		"cursor":        `SELECT count(*) FROM inbox_cursor WHERE inbox = $1`,
		"subscriptions": `SELECT count(*) FROM webhook_subscription WHERE inbox = $1`,
		"deliveries":    `SELECT count(*) FROM webhook_delivery WHERE subscription_id IN ($1 || '-hook', $1 || '-op')`,
		"grants":        `SELECT count(*) FROM access_grant WHERE inbox = $1`,
		"allowlist":     `SELECT count(*) FROM sender_allowlist WHERE inbox = $1`,
		"retention":     `SELECT count(*) FROM retention_policy WHERE scope = 'inbox:' || $1`,
		"created":       `SELECT count(*) FROM idempotency WHERE kind = 'created' AND payload = $1`,
		"sent":          `SELECT count(*) FROM idempotency WHERE kind = 'sent' AND key = 'operator' || chr(10) || $1 || chr(10) || 'key'`,
		"events":        `SELECT count(*) FROM mail_event WHERE inbox = $1`,
		"audit":         `SELECT count(*) FROM audit_event WHERE target = $1`,
	} {
		var n int
		if err := db.pool.QueryRow(ctx, query, inbox).Scan(&n); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = n
	}
	return out
}

var seeded = map[string]int{
	"cursor": 1, "subscriptions": 2, "deliveries": 4, "grants": 4, "allowlist": 1, "retention": 1,
	"created": 1, "sent": 1, "events": 2, "audit": 1,
}

// forgotten is what ForgetInbox leaves: events, audit rows, and send keys.
var forgotten = map[string]int{
	"cursor": 0, "subscriptions": 0, "deliveries": 0, "grants": 0, "allowlist": 0, "retention": 0,
	"created": 0, "sent": 1, "events": 2, "audit": 1,
}

func sameCounts(t *testing.T, label string, got, want map[string]int) {
	t.Helper()
	for name, n := range want {
		if got[name] != n {
			t.Errorf("%s: %s has %d rows, want %d", label, name, got[name], n)
		}
	}
}

func inboxKeys(inbox string) InboxKeys {
	return InboxKeys{Inbox: inbox, RetentionScope: "inbox:" + inbox, CreateKind: "created"}
}

func TestPostgresForgetInboxRemovesItsRowsOnly(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedInbox(t, db, "inbox-a")
	seedInbox(t, db, "inbox-b")
	if err := db.SaveRetention(ctx, "server", 30); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveGrant(ctx, "ada", "*", "inbox:provision"); err != nil {
		t.Fatal(err)
	}
	sameCounts(t, "seeded", inboxRows(t, db, "inbox-a"), seeded)
	if err := db.ForgetInbox(ctx, inboxKeys("inbox-a")); err != nil {
		t.Fatal(err)
	}
	sameCounts(t, "forgotten", inboxRows(t, db, "inbox-a"), forgotten)
	sameCounts(t, "other inbox", inboxRows(t, db, "inbox-b"), seeded)
	if _, ok, err := db.InboxByAddress(ctx, "inbox-a@example.com"); err != nil || ok {
		t.Fatalf("address still bound ok %v err %v", ok, err)
	}
	if days, ok, err := db.Retention(ctx, "server"); err != nil || !ok || days != 30 {
		t.Fatalf("server retention %d ok %v err %v", days, ok, err)
	}
	if ok, err := db.HasGrant(ctx, "ada", "*", "inbox:provision"); err != nil || !ok {
		t.Fatalf("provision grant ok %v err %v", ok, err)
	}
	// A repeat finds nothing to remove and is not an error.
	if err := db.ForgetInbox(ctx, inboxKeys("inbox-a")); err != nil {
		t.Fatalf("repeat %v", err)
	}
	if err := db.ForgetInbox(ctx, InboxKeys{}); err == nil {
		t.Fatal("an empty inbox id was accepted")
	}
	// A new event for the forgotten inbox queues no delivery: its webhooks are gone.
	if err := db.Put(ctx, "inbox-a", "", "s2", []Record{{MessageID: "m3", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if got := inboxRows(t, db, "inbox-a")["deliveries"]; got != 0 {
		t.Fatalf("deliveries after a new event %d", got)
	}
}

// A failure part way through removes nothing, and a repeat finishes the job.
func TestPostgresForgetInboxIsOneTransaction(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedInbox(t, db, "inbox-a")
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	short, err := Open(ctx, withParam(dsn, "lock_timeout", "200"))
	if err != nil {
		t.Fatal(err)
	}
	defer short.Close()
	// sender_allowlist is deleted after the subscriptions, cursor, and grants.
	lock, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `LOCK TABLE sender_allowlist IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	err = short.ForgetInbox(ctx, inboxKeys("inbox-a"))
	_ = lock.Rollback(ctx)
	if err == nil {
		t.Fatal("forget succeeded while sender_allowlist was locked")
	}
	sameCounts(t, "after the failed forget", inboxRows(t, db, "inbox-a"), seeded)
	if err := short.ForgetInbox(ctx, inboxKeys("inbox-a")); err != nil {
		t.Fatal(err)
	}
	sameCounts(t, "after the retry", inboxRows(t, db, "inbox-a"), forgotten)
}

// withParam adds a connection parameter to a URL or keyword/value DSN.
func withParam(dsn, key, value string) string {
	if !strings.Contains(dsn, "://") {
		return dsn + " " + key + "=" + value
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&" + key + "=" + value
	}
	return dsn + "?" + key + "=" + value
}
