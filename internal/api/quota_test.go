// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

type quotaMail struct {
	fakeMail
	quotas map[string]mailbackend.StorageQuota
}

func (q *quotaMail) GetStorageQuota(_ context.Context, id string) (mailbackend.StorageQuota, error) {
	return q.quotas[id], nil
}

func (q *quotaMail) SetStorageQuota(_ context.Context, id string, limitBytes int64) error {
	if q.quotas == nil {
		q.quotas = map[string]mailbackend.StorageQuota{}
	}
	cur := q.quotas[id]
	cur.LimitBytes = limitBytes
	q.quotas[id] = cur
	return nil
}

func TestStorageQuotaRoutes(t *testing.T) {
	mail := &quotaMail{quotas: map[string]mailbackend.StorageQuota{
		"inbox-1": {LimitBytes: 104857600, UsedBytes: 512},
	}}
	srv := &Server{
		Service:  service.New(mail),
		Token:    "secret",
		Domain:   "mail.example",
		DomainID: "d",
		RoleID:   "r",
	}
	h := srv.Handler()

	// GET returns current quota.
	req := httptest.NewRequest(http.MethodGet, "/v1/inboxes/inbox-1/quota", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status %d: %s", rec.Code, rec.Body.String())
	}
	var got mailbackend.StorageQuota
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.LimitBytes != 104857600 || got.UsedBytes != 512 {
		t.Fatalf("get body %s err %v", rec.Body.String(), err)
	}

	// PUT updates quota.
	req = httptest.NewRequest(http.MethodPut, "/v1/inboxes/inbox-1/quota", strings.NewReader(`{"limitBytes":2048}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.LimitBytes != 2048 {
		t.Fatalf("put body %s err %v", rec.Body.String(), err)
	}

	// Missing limitBytes returns 400.
	req = httptest.NewRequest(http.MethodPut, "/v1/inboxes/inbox-1/quota", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing field status %d", rec.Code)
	}

	// Unsupported backend returns 501.
	plain := (&Server{Service: service.New(&fakeMail{}), Token: "secret"}).Handler()
	req = httptest.NewRequest(http.MethodGet, "/v1/inboxes/inbox-1/quota", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	plain.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unsupported status %d: %s", rec.Code, rec.Body.String())
	}
}
