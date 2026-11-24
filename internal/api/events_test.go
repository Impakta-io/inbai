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

	hook := call(t, srv.URL, http.MethodPost, "/v1/webhooks", "operator-token", `{"inbox":"alice-id","url":"https://198.51.100.7/hook"}`, "")
	if hook.StatusCode != http.StatusCreated || !strings.Contains(readBody(t, hook), `"secret"`) {
		t.Fatalf("webhook %d", hook.StatusCode)
	}

	for _, internal := range []string{"http://127.0.0.1:5432/", "https://169.254.169.254/latest", "https://10.0.0.2/hook"} {
		refused := call(t, srv.URL, http.MethodPost, "/v1/webhooks", "operator-token", `{"inbox":"alice-id","url":"`+internal+`"}`, "")
		if refused.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: webhook %d, want 400", internal, refused.StatusCode)
		}
	}

	bounce, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/hooks/ingest", strings.NewReader(`{"events":[{"type":"delivery.dsn-temp-fail","data":{"to":"soft@example.com","details":"451 later"}},{"type":"delivery.dsn-perm-fail","data":{"to":"bad@example.com","details":"550 user unknown"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	bounce.Header.Set("Content-Type", "application/json")
	bounce.Header.Set("X-Inbai-Ingest", "hint")
	res, err = http.DefaultClient.Do(bounce)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusAccepted || !strings.Contains(readBody(t, res), `"suppressed":1`) {
		t.Fatalf("bounce %d", res.StatusCode)
	}
	denied := call(t, srv.URL, http.MethodPost, "/v1/inboxes/alice-id/messages", "operator-token", `{"from":"alice@example.com","to":"bad@example.com","subject":"hi","text":"hi"}`, "")
	if denied.StatusCode != http.StatusConflict || mail.sent != 0 {
		t.Fatalf("suppressed send %d sent %d", denied.StatusCode, mail.sent)
	}
	allowed := call(t, srv.URL, http.MethodPost, "/v1/inboxes/alice-id/messages", "operator-token", `{"from":"alice@example.com","to":"soft@example.com","subject":"hi","text":"hi"}`, "")
	if allowed.StatusCode != http.StatusAccepted || mail.sent != 1 {
		t.Fatalf("soft send %d sent %d", allowed.StatusCode, mail.sent)
	}
	list := call(t, srv.URL, http.MethodGet, "/v1/suppressions", "operator-token", "", "")
	if list.StatusCode != http.StatusOK || !strings.Contains(readBody(t, list), "bad@example.com") {
		t.Fatalf("list %d", list.StatusCode)
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
