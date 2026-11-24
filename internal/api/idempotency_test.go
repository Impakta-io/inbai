// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

// heldMail holds every send until release is closed.
type heldMail struct {
	*fakeMail
	entered chan struct{}
	release chan struct{}
	sends   atomic.Int32
}

func (h *heldMail) Deliver(context.Context, mailbackend.Outbound) error {
	h.entered <- struct{}{}
	<-h.release
	h.sends.Add(1)
	return nil
}

func TestIdempotencyKeyInProgressAndReused(t *testing.T) {
	mail := &heldMail{fakeMail: &fakeMail{}, entered: make(chan struct{}, 8), release: make(chan struct{})}
	handler := (&Server{Service: service.New(mail), Token: "operator-token", Domain: "example.com"}).Handler()
	send := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/inboxes/alice-id/messages", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer operator-token")
		req.Header.Set("Idempotency-Key", "key-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- send(sendJSON) }()
	select {
	case <-mail.entered:
	case <-time.After(5 * time.Second):
		close(mail.release)
		t.Fatal("the first send did not reach the backend")
	}
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() { second <- send(sendJSON) }()
	var busy *httptest.ResponseRecorder
	select {
	case busy = <-second:
	case <-mail.entered:
		close(mail.release)
		t.Fatal("a repeat with the same key reached the backend while the first was in flight")
	case <-time.After(5 * time.Second):
		close(mail.release)
		t.Fatal("the repeat did not answer")
	}
	close(mail.release)
	if busy.Code != http.StatusConflict || !strings.Contains(busy.Body.String(), "still in progress; retry later") {
		t.Fatalf("repeat in flight %d %s", busy.Code, busy.Body.String())
	}
	if done := <-first; done.Code != http.StatusAccepted {
		t.Fatalf("first send %d %s", done.Code, done.Body.String())
	}
	if again := send(sendJSON); again.Code != http.StatusAccepted {
		t.Fatalf("repeat after done %d %s", again.Code, again.Body.String())
	}
	reused := send(strings.Replace(sendJSON, `"hi"`, `"other"`, 1))
	if reused.Code != http.StatusUnprocessableEntity || !strings.Contains(reused.Body.String(), "used for a different request") {
		t.Fatalf("reused key %d %s", reused.Code, reused.Body.String())
	}
	if got := mail.sends.Load(); got != 1 {
		t.Fatalf("sends = %d, want 1", got)
	}
}
