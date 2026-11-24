// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/mailbackend/stalwart"
	"github.com/Impakta-io/inbai/internal/store"
)

// placedMail is a backend that names where it creates inboxes, as stalwart.Mail does.
type placedMail struct {
	*fakeMail
}

func (placedMail) InboxPlacement() (string, string) { return "dom", "inbox-role" }

var _ inboxPlacer = stalwart.Mail{}

func TestCreateKeyStoredWithSpecPlacementStillMatches(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		ctx := context.Background()
		mail := placedMail{fakeMail: &fakeMail{created: "new-id"}}
		svc := open(mail)
		// The hash a server stored when the API put the domain and role ids in each InboxSpec.
		sum := sha256.Sum256([]byte(`{"op":"create","domainId":"dom","roleId":"inbox-role","local":"alice","quotaBytes":0}`))
		stored := hex.EncodeToString(sum[:])
		if got := createHash(mail, mailbackend.InboxSpec{Local: "alice"}); got != stored {
			t.Fatalf("create hash %s, want the stored %s", got, stored)
		}
		hold, _, _, err := svc.reserve(ctx, kindCreate, idempotencyKey(operator, "alice", "old"), stored)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.complete(ctx, hold, "old-id"); err != nil {
			t.Fatal(err)
		}
		id, err := svc.CreateInbox(ctx, operator, mailbackend.InboxSpec{Local: "alice"}, "old")
		if err != nil || id != "old-id" || mail.creates != 0 {
			t.Fatalf("repeat of a stored create id %q err %v creates %d", id, err, mail.creates)
		}
		if _, err := svc.CreateInbox(ctx, operator, mailbackend.InboxSpec{Local: "alice", QuotaBytes: 1}, "old"); !errors.Is(err, ErrKeyReused) {
			t.Fatalf("different create err = %v, want ErrKeyReused", err)
		}
		// A backend that names no placement hashes the same create differently.
		if createHash(&fakeMail{}, mailbackend.InboxSpec{Local: "alice"}) == stored {
			t.Fatal("unplaced backend matched a placed create")
		}
	})
}

// gateMail is a backend whose Deliver waits on release when gated, so a test can hold a send in flight.
type gateMail struct {
	*fakeMail
	mu        sync.Mutex
	gated     bool
	entered   chan struct{}
	release   chan struct{}
	delivered []mailbackend.Outbound
	failNext  int
}

func newGateMail() *gateMail {
	return &gateMail{
		fakeMail: &fakeMail{created: "id-1", messages: []mailbackend.Message{{ID: "m", Subject: "hello", From: "ada@sender.test", MessageID: []string{"<m@sender.test>"}}}},
		entered:  make(chan struct{}, 64),
		release:  make(chan struct{}),
	}
}

func (g *gateMail) Deliver(_ context.Context, msg mailbackend.Outbound) error {
	g.mu.Lock()
	gated := g.gated
	g.mu.Unlock()
	if gated {
		g.entered <- struct{}{}
		<-g.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failNext > 0 {
		g.failNext--
		return errors.New("backend unavailable")
	}
	g.delivered = append(g.delivered, msg)
	return nil
}

func (g *gateMail) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.delivered)
}

// idempotencyBackends runs a test once with keys in memory and once in PostgreSQL
// when INBAI_TEST_DATABASE is set. open returns a fresh service over mail.
func idempotencyBackends(t *testing.T, run func(t *testing.T, open func(mailbackend.Backend) *Service)) {
	t.Run("memory", func(t *testing.T) {
		run(t, New)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("INBAI_TEST_DATABASE")
		if dsn == "" {
			t.Skip("INBAI_TEST_DATABASE is not set")
		}
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`) }()
		db, err := store.Open(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := pool.Exec(ctx, `TRUNCATE idempotency, quota_counter, job_queue, bulk_job`); err != nil {
			t.Fatal(err)
		}
		run(t, func(mail mailbackend.Backend) *Service {
			svc := New(mail)
			svc.SetDB(db)
			return svc
		})
	})
}

var operator = Caller{Operator: true}

func outbound(subject string) mailbackend.Outbound {
	return mailbackend.Outbound{AccountID: "inbox-a", To: "bob@example.com", Subject: subject, Body: "body"}
}

func TestConcurrentSendsWithOneKeyDeliverOnce(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		mail.gated = true
		svc := open(mail)
		const n = 5
		results := make(chan error, n)
		for range n {
			go func() { results <- svc.Deliver(context.Background(), operator, outbound("hi"), "retry-1") }()
		}
		released := false
		defer func() {
			if !released {
				close(mail.release)
			}
		}()
		select {
		case <-mail.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("no send reached the backend")
		}
		// One send is held in the backend. Every other request must answer without reaching it.
		for range n - 1 {
			select {
			case err := <-results:
				if !errors.Is(err, ErrInProgress) {
					t.Errorf("concurrent repeat err = %v, want ErrInProgress", err)
				}
			case <-mail.entered:
				t.Fatal("a second send with the same key reached the backend")
			case <-time.After(5 * time.Second):
				t.Fatal("a concurrent repeat did not answer")
			}
		}
		close(mail.release)
		released = true
		if err := <-results; err != nil {
			t.Fatalf("held send err = %v", err)
		}
		mail.mu.Lock()
		mail.gated = false
		mail.mu.Unlock()
		if err := svc.Deliver(context.Background(), operator, outbound("hi"), "retry-1"); err != nil {
			t.Fatalf("repeat after done err = %v", err)
		}
		if got := mail.count(); got != 1 {
			t.Fatalf("deliveries = %d, want 1", got)
		}
	})
}

func TestKeyReusedForADifferentRequest(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		svc := open(mail)
		ctx := context.Background()
		if err := svc.Deliver(ctx, operator, outbound("first"), "k"); err != nil {
			t.Fatal(err)
		}
		other := outbound("first")
		other.Attachments = []mailbackend.File{{Name: "a.txt", Type: "text/plain", Data: []byte("x")}}
		for name, msg := range map[string]mailbackend.Outbound{
			"subject":    outbound("second"),
			"attachment": other,
		} {
			if err := svc.Deliver(ctx, operator, msg, "k"); !errors.Is(err, ErrKeyReused) {
				t.Fatalf("different %s err = %v, want ErrKeyReused", name, err)
			}
		}
		if got := mail.count(); got != 1 {
			t.Fatalf("deliveries = %d, want 1", got)
		}
		if _, err := svc.CreateInbox(ctx, operator, mailbackend.InboxSpec{Local: "alice"}, "c"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CreateInbox(ctx, operator, mailbackend.InboxSpec{Local: "alice", QuotaBytes: 1}, "c"); !errors.Is(err, ErrKeyReused) {
			t.Fatalf("create with a different quota err = %v, want ErrKeyReused", err)
		}
		id, err := svc.CreateInbox(ctx, operator, mailbackend.InboxSpec{Local: "alice"}, "c")
		if err != nil || id != "id-1" || mail.creates != 1 {
			t.Fatalf("create repeat id %q err %v creates %d", id, err, mail.creates)
		}
	})
}

func TestSendAndReplyKeysAreSeparate(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		svc := open(mail)
		ctx := context.Background()
		if err := svc.Deliver(ctx, operator, outbound("hi"), "same"); err != nil {
			t.Fatal(err)
		}
		if err := svc.Reply(ctx, operator, "inbox-a", "m", mailbackend.Outbound{Body: "thanks"}, "same"); err != nil {
			t.Fatal(err)
		}
		if err := svc.Reply(ctx, operator, "inbox-a", "m", mailbackend.Outbound{Body: "thanks"}, "same"); err != nil {
			t.Fatal(err)
		}
		if got := mail.count(); got != 2 {
			t.Fatalf("deliveries = %d, want a send and a reply", got)
		}
		if mail.delivered[1].To != "ada@sender.test" {
			t.Fatalf("reply went to %q", mail.delivered[1].To)
		}
	})
}

func TestFailedSendReleasesTheKey(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		mail.failNext = 1
		svc := open(mail)
		ctx := context.Background()
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err == nil {
			t.Fatal("the failed send returned nil")
		}
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err != nil {
			t.Fatalf("retry after a failure err = %v", err)
		}
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err != nil {
			t.Fatalf("repeat after the retry err = %v", err)
		}
		if got := mail.count(); got != 1 {
			t.Fatalf("deliveries = %d, want 1", got)
		}
	})
}

func TestStaleReservationIsTakenOver(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		ctx := context.Background()
		mail := newGateMail()
		svc := open(mail)
		start := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		now := start
		svc.now = func() time.Time { return now }
		msg := outbound("hi")
		msg.From = "inbox-a@example.com"
		key := idempotencyKey(operator, "inbox-a", "k")
		// A process reserved the key and stopped before the send finished.
		if _, _, _, err := svc.reserve(ctx, kindSend, key, sendHash("send", "", msg)); err != nil {
			t.Fatal(err)
		}
		now = start.Add(idempotencyTakeover - time.Minute)
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); !errors.Is(err, ErrInProgress) {
			t.Fatalf("fresh reservation err = %v, want ErrInProgress", err)
		}
		if err := svc.Deliver(ctx, operator, outbound("other"), "k"); !errors.Is(err, ErrKeyReused) {
			t.Fatalf("different request err = %v, want ErrKeyReused", err)
		}
		now = start.Add(idempotencyTakeover + time.Minute)
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err != nil {
			t.Fatalf("takeover err = %v", err)
		}
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err != nil {
			t.Fatalf("repeat after takeover err = %v", err)
		}
		if got := mail.count(); got != 1 {
			t.Fatalf("deliveries = %d, want 1", got)
		}
		// The stopped process cannot drop a key another request took over.
		svc.release(ctx, claim{kind: kindSend, key: key, at: start})
		if err := svc.Deliver(ctx, operator, outbound("hi"), "k"); err != nil || mail.count() != 1 {
			t.Fatalf("after a stale release err = %v deliveries %d", err, mail.count())
		}
	})
}

// CreateInboxes waits on release when gated, fails while failNext is set, and otherwise answers as fakeMail.
func (g *gateMail) CreateInboxes(ctx context.Context, specs []mailbackend.InboxSpec) ([]string, error) {
	g.mu.Lock()
	gated := g.gated
	g.mu.Unlock()
	if gated {
		g.entered <- struct{}{}
		<-g.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failNext > 0 {
		g.failNext--
		return nil, errors.New("backend unavailable")
	}
	return g.fakeMail.CreateInboxes(ctx, specs)
}

func (g *gateMail) batchCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.batches)
}

func generateRequest(prefix string, count int) GenerateRequest {
	return GenerateRequest{Prefix: prefix, Domain: "example.com", Count: count}
}

func TestGenerateHashNamesTheBackendPlacement(t *testing.T) {
	sum := sha256.Sum256([]byte(`{"op":"generate","domain":"example.com","domainId":"dom","roleId":"inbox-role","prefix":"u","count":2,"quotaBytes":0}`))
	if got, want := generateHash(placedMail{fakeMail: &fakeMail{}}, "u", generateRequest("u", 2)), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("generate hash %s, want %s", got, want)
	}
	if generateHash(&fakeMail{}, "u", generateRequest("u", 2)) == generateHash(placedMail{fakeMail: &fakeMail{}}, "u", generateRequest("u", 2)) {
		t.Fatal("unplaced backend matched a placed generate")
	}
}

func TestConcurrentGeneratesWithOneKeyRunOnce(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		mail.gated = true
		svc := open(mail)
		const n = 5
		type answer struct {
			result GenerateResult
			err    error
		}
		results := make(chan answer, n)
		for range n {
			go func() {
				got, err := svc.Generate(context.Background(), operator, generateRequest("u", 2), "gen-1")
				results <- answer{got, err}
			}()
		}
		released := false
		defer func() {
			if !released {
				close(mail.release)
			}
		}()
		select {
		case <-mail.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("no generate reached the backend")
		}
		// One generate is held in the backend. Every other request must answer without reaching it.
		for range n - 1 {
			select {
			case got := <-results:
				if !errors.Is(got.err, ErrInProgress) {
					t.Errorf("concurrent repeat %+v err = %v, want ErrInProgress", got.result, got.err)
				}
			case <-mail.entered:
				t.Fatal("a second generate with the same key reached the backend")
			case <-time.After(5 * time.Second):
				t.Fatal("a concurrent repeat did not answer")
			}
		}
		close(mail.release)
		released = true
		first := <-results
		if first.err != nil || len(first.result.Inboxes) != 2 {
			t.Fatalf("held generate %+v err = %v", first.result, first.err)
		}
		mail.mu.Lock()
		mail.gated = false
		mail.mu.Unlock()
		again, err := svc.Generate(context.Background(), operator, generateRequest("u", 2), "gen-1")
		if err != nil || len(again.Inboxes) != 2 || again.Inboxes[0] != first.result.Inboxes[0] {
			t.Fatalf("repeat after done %+v err = %v", again, err)
		}
		if got := mail.batchCount(); got != 1 {
			t.Fatalf("backend batches = %d, want 1", got)
		}
	})
}

func TestGenerateKeyReusedForADifferentRequest(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		svc := open(mail)
		ctx := context.Background()
		if _, err := svc.Generate(ctx, operator, generateRequest("u", 2), "k"); err != nil {
			t.Fatal(err)
		}
		quota := generateRequest("u", 2)
		quota.QuotaBytes = 1
		for name, req := range map[string]GenerateRequest{
			"prefix": generateRequest("v", 2),
			"count":  generateRequest("u", 3),
			"quota":  quota,
		} {
			if got, err := svc.Generate(ctx, operator, req, "k"); !errors.Is(err, ErrKeyReused) {
				t.Fatalf("different %s %+v err = %v, want ErrKeyReused", name, got, err)
			}
		}
		if got := mail.batchCount(); got != 1 {
			t.Fatalf("backend batches = %d, want 1", got)
		}
		// The prefix is compared as it is used, so case and spaces do not make a different request.
		if got, err := svc.Generate(ctx, operator, generateRequest(" U ", 2), "k"); err != nil || len(got.Inboxes) != 2 {
			t.Fatalf("same request %+v err = %v", got, err)
		}
	})
}

func TestRepeatedGenerateReturnsTheSameInboxes(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		mail.taken = map[string]bool{"u00001": true}
		svc := open(mail)
		ctx := context.Background()
		first, err := svc.Generate(ctx, operator, generateRequest("u", 3), "k")
		if err != nil || len(first.Inboxes) != 2 || len(first.Existing) != 1 {
			t.Fatalf("generate %+v err = %v", first, err)
		}
		mail.mu.Lock()
		mail.failNext = 1 // a repeat that reached the backend would fail
		mail.mu.Unlock()
		again, err := svc.Generate(ctx, operator, generateRequest("u", 3), "k")
		if err != nil || again.JobID != "" || len(again.Inboxes) != 2 || again.Inboxes[1] != first.Inboxes[1] ||
			len(again.Existing) != 1 || again.Existing[0] != "u00001@example.com" {
			t.Fatalf("repeat %+v err = %v, want %+v", again, err, first)
		}
		if got := mail.batchCount(); got != 1 {
			t.Fatalf("backend batches = %d, want 1", got)
		}
	})
}

func TestRepeatedQueuedGenerateReturnsTheSameJob(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		svc := open(newGateMail())
		svc.SetSyncLimit(1)
		ctx := context.Background()
		first, err := svc.Generate(ctx, operator, generateRequest("q", 2), "k")
		if err != nil || first.JobID == "" {
			t.Fatalf("generate %+v err = %v", first, err)
		}
		for range 2 {
			again, err := svc.Generate(ctx, operator, generateRequest("q", 2), "k")
			if err != nil || again.JobID != first.JobID || len(again.Inboxes) != 0 {
				t.Fatalf("repeat %+v err = %v, want job %s", again, err, first.JobID)
			}
		}
		if _, err := svc.Generate(ctx, operator, generateRequest("q", 3), "k"); !errors.Is(err, ErrKeyReused) {
			t.Fatalf("different count err = %v, want ErrKeyReused", err)
		}
		if svc.db != nil {
			claimed, ok, err := svc.db.ClaimJob(ctx, svc.now().UTC().Add(time.Second), time.Minute)
			if err != nil || !ok || claimed.UniqueKey != first.JobID {
				t.Fatalf("claim %+v ok %v err %v", claimed, ok, err)
			}
			if extra, ok, err := svc.db.ClaimJob(ctx, svc.now().UTC().Add(time.Second), time.Minute); err != nil || ok {
				t.Fatalf("a second job was queued: %+v err %v", extra, err)
			}
			return
		}
		waitForJob(t, svc, first.JobID)
		svc.mu.Lock()
		jobs := len(svc.jobs)
		svc.mu.Unlock()
		if jobs != 1 {
			t.Fatalf("jobs = %d, want 1", jobs)
		}
	})
}

func TestFailedGenerateReleasesTheKey(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		mail.failNext = 1
		svc := open(mail)
		ctx := context.Background()
		if _, err := svc.Generate(ctx, operator, generateRequest("u", 2), "k"); err == nil {
			t.Fatal("the failed generate returned nil")
		}
		// The released key is free, so a different request may use it now.
		if got, err := svc.Generate(ctx, operator, generateRequest("v", 2), "k"); err != nil || len(got.Inboxes) != 2 {
			t.Fatalf("retry after a failure %+v err = %v", got, err)
		}
		if _, err := svc.Generate(ctx, operator, generateRequest("v", 2), "k"); err != nil {
			t.Fatalf("repeat after the retry err = %v", err)
		}
		if got := mail.batchCount(); got != 1 {
			t.Fatalf("backend batches = %d, want 1", got)
		}
	})
}

func TestGenerateKeyStoredBeforeReservationStillAnswers(t *testing.T) {
	idempotencyBackends(t, func(t *testing.T, open func(mailbackend.Backend) *Service) {
		mail := newGateMail()
		svc := open(mail)
		if svc.db == nil {
			t.Skip("keys held in memory do not outlive the process")
		}
		ctx := context.Background()
		// An older server keyed a generate by caller, prefix, and count, with no request hash.
		stored := `{"Inboxes":[{"id":"old-0","address":"u00000@example.com"}],"Existing":["u00001@example.com"],"JobID":""}`
		if err := svc.db.SaveIdempotency(ctx, "generate", "operator\nu\n2\nold", stored); err != nil {
			t.Fatal(err)
		}
		got, err := svc.Generate(ctx, operator, generateRequest("u", 2), "old")
		if err != nil || len(got.Inboxes) != 1 || got.Inboxes[0].ID != "old-0" || len(got.Existing) != 1 {
			t.Fatalf("old key %+v err = %v", got, err)
		}
		if got := mail.batchCount(); got != 0 {
			t.Fatalf("backend batches = %d, want 0", got)
		}
		// The answer is now bound to that request.
		if _, err := svc.Generate(ctx, operator, generateRequest("w", 2), "old"); !errors.Is(err, ErrKeyReused) {
			t.Fatalf("different prefix err = %v, want ErrKeyReused", err)
		}
	})
}
