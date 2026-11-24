// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Impakta-io/inbai/internal/api"
	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/jobs"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestTwoReplicasShareStateAndJobs(t *testing.T) {
	dsn := os.Getenv("INBAI_TEST_DATABASE")
	if dsn == "" {
		t.Skip("INBAI_TEST_DATABASE is not set")
	}
	ctx := context.Background()
	lockDatabase(t, dsn)

	dbA, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer dbA.Close()
	resetDatabase(t, dsn)

	dbB, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer dbB.Close()

	// Both replicas adopt the signing key from PostgreSQL without a shared flag.
	keyA, err := dbA.AdoptSigningKey(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := dbB.AdoptSigningKey(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(keyA) != string(keyB) {
		t.Fatal("replicas did not adopt the same signing key")
	}

	mail := newSharedMail()
	const issuer = "http://inbai.local"
	const opToken = "replica-operator-token"

	startReplica := func(db *store.Postgres) (*httptest.Server, *service.Service, *events.Feed, *jobs.Worker) {
		svc := service.New(mail)
		svc.SetDB(db)
		svc.SetSyncLimit(2)
		feed := events.New(db)
		// The receiver listens on loopback, so the operator policy must allow it,
		// as INBAI_WEBHOOK_ALLOW_PRIVATE=true does in cmd/inbaid.
		feed.SetAllowPrivate(true)
		svc.SetFeed(feed)
		dir := auth.NewDirectory(keyA, issuer)
		dir.SetDB(db)
		srv := httptest.NewServer((&api.Server{
			Service:       svc,
			Directory:     dir,
			Token:         opToken,
			Issuer:        issuer,
			Domain:        "example.com",
			DomainID:      "dom",
			RoleID:        "role",
			RetentionDays: 30,
		}).Handler())
		worker := jobs.New(db)
		worker.Handle(jobs.KindBulkGenerate, func(ctx context.Context, raw json.RawMessage) error {
			var payload service.BulkPayload
			if err := json.Unmarshal(raw, &payload); err != nil {
				return jobs.Permanent(err)
			}
			if err := svc.RunBulkJob(ctx, payload); err != nil {
				return jobs.Permanent(err)
			}
			return nil
		})
		worker.Handle(jobs.KindCursorSweep, func(ctx context.Context, _ json.RawMessage) error {
			return feed.Sweep(ctx, mail)
		})
		return srv, svc, feed, worker
	}

	replicaA, _, _, _ := startReplica(dbA)
	defer replicaA.Close()
	replicaB, _, feedB, workerB := startReplica(dbB)
	defer replicaB.Close()

	// 1. Create two inboxes on replica A, deliver a message to inbox A.
	var inboxA, inboxB struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	}
	doJSON(t, replicaA.URL, opToken, http.MethodPost, "/v1/inboxes", `{"localPart":"ada"}`, http.StatusCreated, &inboxA)
	doJSON(t, replicaA.URL, opToken, http.MethodPost, "/v1/inboxes", `{"localPart":"bea"}`, http.StatusCreated, &inboxB)
	mail.deliver(inboxA.ID, mailbackend.Message{ID: "m-1", Subject: "welcome", Preview: "hi", Text: "hello ada"})
	mail.deliver(inboxB.ID, mailbackend.Message{ID: "m-2", Subject: "private", Preview: "secret", Text: "only bea"})

	// 2. Issue agent credentials on replica A, grant read on inbox A, verify on replica B.
	var cred struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	doJSON(t, replicaA.URL, opToken, http.MethodPost, "/v1/agents", `{"name":"ada"}`, http.StatusCreated, &cred)
	doJSON(t, replicaA.URL, opToken, http.MethodPost, "/v1/grants",
		fmt.Sprintf(`{"agent":"ada","inbox":%q,"permission":"read"}`, inboxA.ID), http.StatusCreated, nil)

	token := exchangeToken(t, replicaA.URL, cred.ID, cred.Secret)

	// Replica B accepts the token minted by replica A and enforces isolation on REST and MCP.
	var listA struct {
		Messages []mailbackend.Message `json:"messages"`
	}
	doJSON(t, replicaB.URL, token, http.MethodGet, "/v1/inboxes/"+inboxA.ID+"/messages", "", http.StatusOK, &listA)
	if len(listA.Messages) != 1 || listA.Messages[0].Subject != "welcome" {
		t.Fatalf("replica B list %+v", listA.Messages)
	}
	doJSON(t, replicaB.URL, token, http.MethodGet, "/v1/inboxes/"+inboxB.ID+"/messages", "", http.StatusForbidden, nil)

	mcpBody := doRaw(t, replicaB.URL, token, http.MethodPost, "/mcp",
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":%q}}}`, inboxB.ID),
		http.StatusOK)
	if !strings.Contains(mcpBody, `"isError":true`) {
		t.Fatalf("replica B MCP allowed other inbox: %s", mcpBody)
	}

	// 3. Register a webhook on replica A, stop replica A, deliver a message, then sweep and post from replica B.
	var hookMu sync.Mutex
	var gotSig string
	var gotBody []byte
	hook := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		hookMu.Lock()
		gotSig = r.Header.Get("X-Inbai-Signature")
		gotBody = raw
		hookMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer hook.Close()

	var sub struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	doJSON(t, replicaA.URL, token, http.MethodPost, "/v1/webhooks",
		fmt.Sprintf(`{"inbox":%q,"url":%q}`, inboxA.ID, hook.URL), http.StatusCreated, &sub)

	// Bulk generate on replica A above sync limit (2); replica B's worker claims and completes it.
	var gen struct {
		JobID string `json:"jobId"`
	}
	doJSON(t, replicaA.URL, opToken, http.MethodPost, "/v1/inboxes/generate",
		`{"prefix":"bulk","count":4}`, http.StatusAccepted, &gen)
	if gen.JobID == "" {
		t.Fatal("expected bulk job id")
	}

	// Stop replica A while a new message arrives and bulk work is queued.
	replicaA.Close()
	mail.deliver(inboxA.ID, mailbackend.Message{ID: "m-3", Subject: "while down", Preview: "queued", Text: "not lost"})

	// Replica B enqueues a singleton cursor sweep and runs queued jobs (bulk + sweep).
	if _, _, err := dbB.Enqueue(ctx, jobs.KindCursorSweep, "singleton", []byte(`{}`), time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		ok, err := workerB.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	for range 4 {
		ok, err := feedB.DeliverOnce(ctx, hook.Client()) // hook.Client trusts the test certificate
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}

	var job struct {
		Status  string `json:"status"`
		Created int    `json:"created"`
	}
	doJSON(t, replicaB.URL, opToken, http.MethodGet, "/v1/jobs/"+gen.JobID, "", http.StatusOK, &job)
	if job.Status != "done" || job.Created != 4 {
		t.Fatalf("bulk job on replica B: %+v", job)
	}

	var evts struct {
		Events []store.Record `json:"events"`
	}
	doJSON(t, replicaB.URL, token, http.MethodGet, "/v1/events?inbox="+url.QueryEscape(inboxA.ID)+"&after=0&timeout=1", "", http.StatusOK, &evts)
	foundEvent := false
	for _, ev := range evts.Events {
		if ev.MessageID == "m-3" && ev.Kind == "created" {
			foundEvent = true
		}
	}
	if !foundEvent {
		t.Fatalf("events after replica A stopped: %+v", evts.Events)
	}

	hookMu.Lock()
	sig, payload := gotSig, append([]byte(nil), gotBody...)
	hookMu.Unlock()
	if events.Sign(sub.Secret, payload) != sig || !strings.Contains(string(payload), "m-3") {
		t.Fatalf("webhook sig=%q body=%s", sig, payload)
	}

	// 4. Suspend ada on replica B and confirm the already-issued token is rejected immediately.
	doJSON(t, replicaB.URL, opToken, http.MethodPost, "/v1/agents/ada/suspend", "", http.StatusNoContent, nil)
	doJSON(t, replicaB.URL, token, http.MethodGet, "/v1/inboxes/"+inboxA.ID+"/messages", "", http.StatusUnauthorized, nil)
}

func lockDatabase(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
		conn.Release()
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`)
		conn.Release()
		pool.Close()
	})
}

func resetDatabase(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `TRUNCATE webhook_delivery, webhook_subscription, mail_event, inbox_cursor, suppression, access_grant, quota_counter, idempotency, bulk_job, agent_credential, agent_suspension, audit_event, retention_policy, sender_allowlist, signing_key, job_queue`)
	if err != nil {
		t.Fatal(err)
	}
}

func exchangeToken(t *testing.T, base, id, secret string) string {
	t.Helper()
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", id)
	form.Set("client_secret", secret)
	res, err := http.Post(base+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("oauth/token %d %s", res.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.AccessToken
}

func doJSON(t *testing.T, base, token, method, path, body string, want int, dest any) {
	t.Helper()
	raw := doRaw(t, base, token, method, path, body, want)
	if dest != nil {
		if err := json.Unmarshal([]byte(raw), dest); err != nil {
			t.Fatalf("%s %s json %s: %v", method, path, raw, err)
		}
	}
}

func doRaw(t *testing.T, base, token, method, path, body string, want int) string {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s status %d want %d body %s", method, path, res.StatusCode, want, raw)
	}
	return string(raw)
}

type sharedMail struct {
	mu       sync.Mutex
	nextID   int
	accounts []mailbackend.Account
	messages map[string][]mailbackend.Message
	states   map[string][]string
}

func newSharedMail() *sharedMail {
	return &sharedMail{
		messages: map[string][]mailbackend.Message{},
		states:   map[string][]string{},
	}
}

func (m *sharedMail) deliver(accountID string, msg mailbackend.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[accountID] = append(m.messages[accountID], msg)
	m.states[accountID] = append(m.states[accountID], msg.ID)
}

func (m *sharedMail) CreateInbox(_ context.Context, spec mailbackend.InboxSpec) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("acct-%d", m.nextID)
	m.accounts = append(m.accounts, mailbackend.Account{ID: id, Local: spec.Local})
	return id, nil
}

func (m *sharedMail) CreateInboxes(ctx context.Context, specs []mailbackend.InboxSpec) ([]string, error) {
	ids := make([]string, len(specs))
	for i, spec := range specs {
		id, err := m.CreateInbox(ctx, spec)
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}
	return ids, nil
}

func (m *sharedMail) Deliver(context.Context, mailbackend.Outbound) error { return nil }

func (m *sharedMail) List(_ context.Context, accountID string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	m.mu.Lock()
	list := append([]mailbackend.Message(nil), m.messages[accountID]...)
	m.mu.Unlock()
	return mailbackend.Slice(list, func(msg mailbackend.Message) string { return msg.ID }, page)
}

func (m *sharedMail) Search(ctx context.Context, accountID, _ string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	return m.List(ctx, accountID, page)
}

func (m *sharedMail) Get(_ context.Context, accountID, messageID string) (mailbackend.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.messages[accountID] {
		if msg.ID == messageID {
			return msg, nil
		}
	}
	return mailbackend.Message{}, fmt.Errorf("not found")
}

func (m *sharedMail) Delete(context.Context, string, string) error { return nil }

func (m *sharedMail) ListInboxes(_ context.Context, page mailbackend.Page) ([]mailbackend.Account, string, error) {
	m.mu.Lock()
	accounts := append([]mailbackend.Account(nil), m.accounts...)
	m.mu.Unlock()
	return mailbackend.Slice(accounts, func(a mailbackend.Account) string { return a.ID }, page)
}

func (m *sharedMail) DeleteInbox(context.Context, string) error { return nil }
func (m *sharedMail) Download(context.Context, string, string) ([]byte, error) {
	return nil, nil
}

func (m *sharedMail) MailState(_ context.Context, accountID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fmt.Sprintf("%d", len(m.states[accountID])), nil
}

func (m *sharedMail) MailChanges(_ context.Context, accountID, since string) (mailbackend.Delta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var start int
	_, _ = fmt.Sscanf(since, "%d", &start)
	all := m.states[accountID]
	if start > len(all) {
		start = len(all)
	}
	return mailbackend.Delta{
		State:   fmt.Sprintf("%d", len(all)),
		Created: append([]string(nil), all[start:]...),
	}, nil
}
