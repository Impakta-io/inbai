// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
)

// refreshServer issues numbered tokens with a 60 second lifetime and accepts
// only the most recently issued one, as if the older ones had expired.
type refreshServer struct {
	mu        sync.Mutex
	exchanges int
	apiCalls  int
	valid     string
	rejectAll bool
	noExpiry  bool
}

func (f *refreshServer) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/oauth/token" {
		f.exchanges++
		f.valid = fmt.Sprintf("tok-%d", f.exchanges)
		w.Header().Set("Content-Type", "application/json")
		if f.noExpiry {
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer"}`, f.valid)
			return
		}
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"expires_in":60,"token_type":"Bearer"}`, f.valid)
		return
	}
	f.apiCalls++
	if f.rejectAll || r.Header.Get("Authorization") != "Bearer "+f.valid {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"invalid token"}`))
		return
	}
	_, _ = w.Write([]byte(`{"inboxes":[]}`))
}

func (f *refreshServer) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exchanges, f.apiCalls
}

func (f *refreshServer) forget() {
	f.mu.Lock()
	f.valid = "revoked"
	f.mu.Unlock()
}

func clientCredentials(t *testing.T, base string, now func() time.Time) apiClient {
	t.Helper()
	getenv := func(k string) string {
		switch k {
		case "INBAI_CLIENT_ID":
			return "id"
		case "INBAI_CLIENT_SECRET":
			return "s3cret"
		}
		return ""
	}
	token, src, err := mcpCredential(context.Background(), base, config.File{}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	src.now = now
	return apiClient{base: base, token: token, src: src}
}

func TestClientCredentialTokenRenewsBeforeExpiry(t *testing.T) {
	f := &refreshServer{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	clock := time.Now()
	c := clientCredentials(t, srv.URL, func() time.Time { return clock })
	ctx := context.Background()
	if _, err := c.call(ctx, http.MethodGet, "/v1/inboxes", nil, nil); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(20 * time.Second)
	if _, err := c.call(ctx, http.MethodGet, "/v1/inboxes", nil, nil); err != nil {
		t.Fatal(err)
	}
	if ex, _ := f.counts(); ex != 1 {
		t.Fatalf("exchanges %d, want 1 while the token is fresh", ex)
	}
	// 40s into a 60s token is inside the 30s margin: renew first, so the API sees no 401.
	clock = clock.Add(20 * time.Second)
	if _, err := c.call(ctx, http.MethodGet, "/v1/inboxes", nil, nil); err != nil {
		t.Fatal(err)
	}
	if ex, api := f.counts(); ex != 2 || api != 3 {
		t.Fatalf("exchanges %d apiCalls %d, want 2 and 3", ex, api)
	}
}

func TestClientCredentialTokenRetriesOnceOn401(t *testing.T) {
	f := &refreshServer{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	c := clientCredentials(t, srv.URL, func() time.Time { return time.Now() })
	f.forget()
	if _, err := c.call(context.Background(), http.MethodGet, "/v1/inboxes", nil, nil); err != nil {
		t.Fatal(err)
	}
	if ex, api := f.counts(); ex != 2 || api != 2 {
		t.Fatalf("exchanges %d apiCalls %d, want 2 and 2", ex, api)
	}
}

func TestClientCredentialSecond401IsReturned(t *testing.T) {
	f := &refreshServer{rejectAll: true}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	c := clientCredentials(t, srv.URL, time.Now)
	status, err := c.call(context.Background(), http.MethodGet, "/v1/inboxes", nil, nil)
	if err == nil || status != http.StatusUnauthorized {
		t.Fatalf("status %d err %v", status, err)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error leaks the secret: %v", err)
	}
	if ex, api := f.counts(); ex != 2 || api != 2 {
		t.Fatalf("exchanges %d apiCalls %d, want 2 and 2 (no retry loop)", ex, api)
	}
}

func TestStaticTokenGetsOneAttemptOn401(t *testing.T) {
	f := &refreshServer{rejectAll: true}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	for _, key := range []string{"INBAI_ACCESS_TOKEN", "INBAI_OPERATOR_TOKEN"} {
		getenv := func(k string) string {
			if k == key {
				return "static"
			}
			return ""
		}
		token, src, err := mcpCredential(context.Background(), srv.URL, config.File{}, getenv)
		if err != nil || src != nil {
			t.Fatalf("%s: src %v err %v", key, src, err)
		}
		c := apiClient{base: srv.URL, token: token, src: src}
		if status, err := c.call(context.Background(), http.MethodGet, "/v1/inboxes", nil, nil); err == nil || status != http.StatusUnauthorized {
			t.Fatalf("%s: status %d err %v", key, status, err)
		}
	}
	if ex, api := f.counts(); ex != 0 || api != 2 {
		t.Fatalf("exchanges %d apiCalls %d, want 0 and 2 (one per client)", ex, api)
	}
}

func TestConcurrentCallsShareOneRenewal(t *testing.T) {
	f := &refreshServer{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	c := clientCredentials(t, srv.URL, time.Now)
	f.forget()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.call(context.Background(), http.MethodGet, "/v1/inboxes", nil, nil)
		}()
	}
	wg.Wait()
	if ex, _ := f.counts(); ex > 2 {
		t.Fatalf("exchanges %d, want 2 (startup plus one shared renewal)", ex)
	}
}

func TestTokenWithoutExpiresInRenewsOnlyOn401(t *testing.T) {
	f := &refreshServer{noExpiry: true}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	clock := time.Now()
	c := clientCredentials(t, srv.URL, func() time.Time { return clock })
	for range 5 {
		clock = clock.Add(time.Hour)
		if _, err := c.call(context.Background(), http.MethodGet, "/v1/inboxes", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if ex, _ := f.counts(); ex != 1 {
		t.Fatalf("exchanges %d, want 1", ex)
	}
	f.forget()
	if _, err := c.call(context.Background(), http.MethodGet, "/v1/inboxes", nil, nil); err != nil {
		t.Fatal(err)
	}
	if ex, _ := f.counts(); ex != 2 {
		t.Fatalf("exchanges %d, want 2", ex)
	}
}
