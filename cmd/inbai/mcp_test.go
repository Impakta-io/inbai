// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/api"
	"github.com/Impakta-io/inbai/internal/events"
	"github.com/Impakta-io/inbai/internal/mcp"
	"github.com/Impakta-io/inbai/internal/service"
	"github.com/Impakta-io/inbai/internal/store"
)

func TestMCPServeExchangesAClientCredential(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"agent-token","expires_in":600,"token_type":"Bearer"}`))
		case "/v1/inboxes/inbox-a/messages":
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"messages":[{"id":"m","subject":"secret","preview":"body"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	path := writeOperatorConfig(t, srv.URL)
	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":"inbox-a"}}}`,
	}, "\n"))
	var stdout, stderr bytes.Buffer
	code := mcpCmd([]string{"serve", "--config", path}, stdin, &stdout, &stderr, func(key string) string {
		switch key {
		case "INBAI_CLIENT_ID":
			return "id"
		case "INBAI_CLIENT_SECRET":
			return "secret"
		default:
			return ""
		}
	})
	if code != 0 {
		t.Fatalf("code %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "2026-07-28") || !strings.Contains(stdout.String(), "secret") || !strings.Contains(stdout.String(), "externalContent") {
		t.Fatalf("stdout %s", stdout.String())
	}
	if gotAuth != "Bearer agent-token" {
		t.Fatalf("auth %q", gotAuth)
	}
}

func TestMCPServeRequiresACredential(t *testing.T) {
	path := writeOperatorConfig(t, "http://127.0.0.1:8787")
	var stdout, stderr bytes.Buffer
	code := mcpCmd([]string{"serve", "--config", path}, strings.NewReader(""), &stdout, &stderr, func(string) string { return "" })
	if code != 2 || !strings.Contains(stderr.String(), "missing credential") {
		t.Fatalf("code %d %s", code, stderr.String())
	}
}

func TestMCPStdioWaitFollowsTheEventCursor(t *testing.T) {
	log := &signalLog{Log: &store.Memory{}, read: make(chan struct{})}
	svc := service.New(nil)
	svc.SetFeed(events.New(log))
	srv := httptest.NewServer((&api.Server{Service: svc, Token: "operator-token"}).Handler())
	defer srv.Close()
	box := apiBox{client: apiClient{base: srv.URL, token: "operator-token"}}
	put := func(ids ...string) {
		t.Helper()
		records := make([]store.Record, len(ids))
		for i, id := range ids {
			records[i] = store.Record{MessageID: id, Kind: "created"}
		}
		if err := log.Put(context.Background(), "inbox-a", "", "", records); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(args string) string {
		raw := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait_for_message","arguments":{"inbox":"inbox-a",` + args + `}}}`
		got, _ := mcp.Dispatch(context.Background(), []byte(raw), box)
		return string(got)
	}
	put("m-old-1", "m-old-2")

	done := make(chan string, 1)
	go func() { done <- wait(`"timeoutSeconds":5`) }()
	<-log.read
	put("m-new")
	got := <-done
	if strings.Contains(got, "m-old") || !strings.Contains(got, `\"id\":\"m-new\"`) || !strings.Contains(got, `\"after\":3`) {
		t.Fatalf("wait without a cursor %s", got)
	}

	put("m-next")
	start := time.Now()
	got = wait(`"after":3,"timeoutSeconds":5`)
	if time.Since(start) > time.Second || strings.Contains(got, "m-new") || !strings.Contains(got, "m-next") || !strings.Contains(got, `\"after\":4`) {
		t.Fatalf("wait with a cursor %s after %s", got, time.Since(start))
	}

	got = wait(`"after":4,"timeoutSeconds":0.1`)
	if strings.Contains(got, `"isError"`) || !strings.Contains(got, `\"messages\":[]`) || !strings.Contains(got, `\"after\":4`) {
		t.Fatalf("wait timeout %s", got)
	}
}

// signalLog closes read after the first head lookup, so a test knows the waiter has its cursor.
type signalLog struct {
	store.Log
	read chan struct{}
	once sync.Once
}

func (s *signalLog) Head(ctx context.Context, inbox string) (int64, error) {
	id, err := s.Log.Head(ctx, inbox)
	s.once.Do(func() { close(s.read) })
	return id, err
}

func TestMCPStdioListMessagesPassesTheCursorAndReturnsNext(t *testing.T) {
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"messages":[{"id":"m2","subject":"newer"}],"next":"m2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"messages":[{"id":"m1","subject":"older"}]}`))
	}))
	defer srv.Close()
	box := apiBox{client: apiClient{base: srv.URL, token: "t"}}
	call := func(args string) string {
		raw := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{` + args + `}}}`
		got, _ := mcp.Dispatch(context.Background(), []byte(raw), box)
		return string(got)
	}
	first := call(`"inbox":"inbox-a"`)
	if !strings.Contains(first, `newer`) || !strings.Contains(first, `next\":\"m2\"`) {
		t.Fatalf("first %s", first)
	}
	second := call(`"inbox":"inbox-a","cursor":"m2"`)
	if !strings.Contains(second, `older`) || !strings.Contains(second, `next\":\"\"`) {
		t.Fatalf("second %s", second)
	}
	if strings.Join(cursors, "|") != "|m2" {
		t.Fatalf("cursors %q", cursors)
	}
}
