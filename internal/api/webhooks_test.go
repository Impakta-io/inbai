// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
)

type hookServer struct {
	api    *httptest.Server
	target *httptest.Server
	hits   *atomic.Int32
	feed   *events.Feed
	log    *store.Memory
}

func newHookServer(t *testing.T) hookServer {
	t.Helper()
	hits := &atomic.Int32{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	log := &store.Memory{}
	feed := events.New(log)
	feed.SetAllowPrivate(true)
	dir := auth.NewDirectory([]byte("signing-key"), "http://127.0.0.1:8787")
	svc := service.New(&fakeMail{})
	svc.SetFeed(feed)
	svc.SetSuspensions(dir)
	api := httptest.NewServer((&Server{Service: svc, Directory: dir, Token: "operator-token", Issuer: "http://127.0.0.1:8787"}).Handler())
	t.Cleanup(api.Close)
	return hookServer{api: api, target: target, hits: hits, feed: feed, log: log}
}

// agent creates an agent with read on inbox-a and returns its access token.
func (h hookServer) agent(t *testing.T, name string) string {
	t.Helper()
	create := operatorJSON(t, h.api.URL, http.MethodPost, "/v1/agents", `{"name":"`+name+`"}`)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("agent %d %s", create.StatusCode, readBody(t, create))
	}
	var cred struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	mustJSON(t, create, &cred)
	grant := operatorJSON(t, h.api.URL, http.MethodPost, "/v1/grants", `{"agent":"`+name+`","inbox":"inbox-a","permission":"read"}`)
	if grant.StatusCode != http.StatusCreated {
		t.Fatalf("grant %d %s", grant.StatusCode, readBody(t, grant))
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {cred.ID}, "client_secret": {cred.Secret}}
	res, err := http.Post(h.api.URL+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	mustJSON(t, res, &tok)
	return tok.AccessToken
}

func (h hookServer) subscribe(t *testing.T, token string) string {
	t.Helper()
	res := agentJSON(t, h.api.URL, token, http.MethodPost, "/v1/webhooks", `{"inbox":"inbox-a","url":"`+h.target.URL+`"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("webhook %d %s", res.StatusCode, readBody(t, res))
	}
	var created struct {
		ID string `json:"id"`
	}
	mustJSON(t, res, &created)
	return created.ID
}

// event logs one change and runs the delivery loop until nothing is due.
func (h hookServer) event(t *testing.T, messageID string) {
	t.Helper()
	if err := h.log.Put(context.Background(), "inbox-a", "", "s", []store.Record{{MessageID: messageID, Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		ok, err := h.feed.DeliverOnce(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return
		}
	}
}

func TestSuspendedAgentWebhookIsNotSent(t *testing.T) {
	h := newHookServer(t)
	ada := h.agent(t, "ada")
	h.subscribe(t, ada)
	h.event(t, "m1")
	if h.hits.Load() != 1 {
		t.Fatalf("hits before suspend %d", h.hits.Load())
	}
	res := operatorJSON(t, h.api.URL, http.MethodPost, "/v1/agents/ada/suspend", "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("suspend %d %s", res.StatusCode, readBody(t, res))
	}
	h.event(t, "m2")
	h.event(t, "m3")
	if h.hits.Load() != 1 {
		t.Fatalf("a suspended agent's webhook was sent %d times", h.hits.Load())
	}
	list := call(t, h.api.URL, http.MethodGet, "/v1/webhooks?inbox=inbox-a", "operator-token", "", "")
	body := readBody(t, list)
	if list.StatusCode != http.StatusOK || !strings.Contains(body, `"active":false`) || !strings.Contains(body, `"disabledReason":"agent suspended"`) {
		t.Fatalf("list %d %s", list.StatusCode, body)
	}
}

func TestRevokedAgentWebhookIsNotSent(t *testing.T) {
	h := newHookServer(t)
	ada := h.agent(t, "ada")
	h.subscribe(t, ada)
	res := call(t, h.api.URL, http.MethodDelete, "/v1/grants/ada/inbox-a/read", "operator-token", "", "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke %d %s", res.StatusCode, readBody(t, res))
	}
	h.event(t, "m1")
	if h.hits.Load() != 0 {
		t.Fatalf("a webhook was sent after read was revoked %d times", h.hits.Load())
	}
}

func TestWebhookListAndDeleteAreOwnerOnly(t *testing.T) {
	h := newHookServer(t)
	ada, bob := h.agent(t, "ada"), h.agent(t, "bob")
	id := h.subscribe(t, ada)

	own := agentGet(t, h.api.URL, ada, "/v1/webhooks?inbox=inbox-a")
	body := readBody(t, own)
	if own.StatusCode != http.StatusOK || !strings.Contains(body, `"id":"`+id+`"`) || !strings.Contains(body, `"active":true`) || strings.Contains(body, "secret") {
		t.Fatalf("own list %d %s", own.StatusCode, body)
	}
	other := agentGet(t, h.api.URL, bob, "/v1/webhooks?inbox=inbox-a")
	if body := readBody(t, other); other.StatusCode != http.StatusOK || body != `{"webhooks":[]}`+"\n" {
		t.Fatalf("other list %d %s", other.StatusCode, body)
	}
	if res := agentGet(t, h.api.URL, ada, "/v1/webhooks"); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("list without inbox %d", res.StatusCode)
	}
	for _, path := range []string{"/v1/webhooks/" + id, "/v1/webhooks/missing"} {
		res := agentJSON(t, h.api.URL, bob, http.MethodDelete, path, "")
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("bob delete %s: %d %s", path, res.StatusCode, readBody(t, res))
		}
	}
	all := call(t, h.api.URL, http.MethodGet, "/v1/webhooks?inbox=inbox-a", "operator-token", "", "")
	if body := readBody(t, all); all.StatusCode != http.StatusOK || !strings.Contains(body, id) || strings.Contains(body, "secret") {
		t.Fatalf("operator list %d %s", all.StatusCode, body)
	}

	// Queue a delivery, then delete: it is not sent.
	if err := h.log.Put(context.Background(), "inbox-a", "", "s", []store.Record{{MessageID: "m1", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	res := agentJSON(t, h.api.URL, ada, http.MethodDelete, "/v1/webhooks/"+id, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete %d %s", res.StatusCode, readBody(t, res))
	}
	h.event(t, "m2")
	if h.hits.Load() != 0 {
		t.Fatalf("a deleted webhook was sent %d times", h.hits.Load())
	}
	if res := agentJSON(t, h.api.URL, ada, http.MethodDelete, "/v1/webhooks/"+id, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete %d", res.StatusCode)
	}
}

func TestWebhookCapIsQuotaExceeded(t *testing.T) {
	h := newHookServer(t)
	ada := h.agent(t, "ada")
	for range service.MaxWebhooksPerInbox {
		h.subscribe(t, ada)
	}
	res := agentJSON(t, h.api.URL, ada, http.MethodPost, "/v1/webhooks", `{"inbox":"inbox-a","url":"`+h.target.URL+`"}`)
	if body := readBody(t, res); res.StatusCode != http.StatusTooManyRequests || !strings.Contains(body, "webhook limit") {
		t.Fatalf("over the cap %d %s", res.StatusCode, body)
	}
	op := call(t, h.api.URL, http.MethodPost, "/v1/webhooks", "operator-token", `{"inbox":"inbox-a","url":"`+h.target.URL+`"}`, "")
	if op.StatusCode != http.StatusCreated {
		t.Fatalf("operator webhook %d %s", op.StatusCode, readBody(t, op))
	}
}
