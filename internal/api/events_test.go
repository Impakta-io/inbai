// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestIngestHintFeedsTheCursor(t *testing.T) {
	mail := &fakeMail{id: "alice-id", messages: []mailbackend.Message{{ID: "m", Subject: "hello"}}}
	svc := service.New(mail)
	svc.SetFeed(events.New(&store.Memory{}))
	srv := httptest.NewServer((&Server{
		Service: svc, Token: "operator-token", Domain: "example.com", DomainID: "dom", RoleID: "role", IngestSecret: "hint",
	}).Handler())
	defer srv.Close()

	res := call(t, srv.URL, http.MethodPost, "/v1/inboxes", "operator-token", `{"localPart":"alice"}`, "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, readBody(t, res))
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/hooks/ingest", strings.NewReader(`{"events":[{"type":"message-ingest.ham","data":{"accountId":4,"to":["alice@example.com"]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Inbai-Ingest", "hint")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusAccepted || !strings.Contains(readBody(t, res), `"synced":1`) {
		t.Fatalf("ingest %d", res.StatusCode)
	}
	res = call(t, srv.URL, http.MethodGet, "/v1/events?inbox=alice-id&timeout=0", "operator-token", "", "")
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"messageId":"m-new"`) || !strings.Contains(body, `"kind":"created"`) {
		t.Fatalf("events %d %s", res.StatusCode, body)
	}

	hook := call(t, srv.URL, http.MethodPost, "/v1/webhooks", "operator-token", `{"inbox":"alice-id","url":"https://agent.example/hook"}`, "")
	if hook.StatusCode != http.StatusCreated || !strings.Contains(readBody(t, hook), `"secret"`) {
		t.Fatalf("webhook %d", hook.StatusCode)
	}
}

func TestHealthAndAMissingEventStore(t *testing.T) {
	srv := httptest.NewServer((&Server{Service: service.New(&fakeMail{}), Token: "operator-token"}).Handler())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || !strings.Contains(readBody(t, res), "ok") {
		t.Fatalf("health %d", res.StatusCode)
	}
	res, err = http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || !strings.Contains(readBody(t, res), "inbai_events_appended_total") {
		t.Fatalf("metrics %d", res.StatusCode)
	}
	res = call(t, srv.URL, http.MethodGet, "/v1/events?inbox=alice-id&timeout=0", "operator-token", "", "")
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("events %d %s", res.StatusCode, readBody(t, res))
	}
}
