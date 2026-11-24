// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Impakta-io/inbai/internal/store"
)

func fixedLookup(addrs ...string) LookupFunc {
	return func(context.Context, string) ([]net.IPAddr, error) {
		out := make([]net.IPAddr, 0, len(addrs))
		for _, a := range addrs {
			out = append(out, net.IPAddr{IP: net.ParseIP(a)})
		}
		return out, nil
	}
}

func TestCheckWebhookURLRefusesInternalTargets(t *testing.T) {
	refused := []string{
		"https://127.0.0.1/hook",
		"https://[::1]/hook",
		"https://10.0.0.5/hook",
		"https://172.16.0.1/hook",
		"https://192.168.1.1/hook",
		"https://169.254.169.254/latest/meta-data",
		"https://[fe80::1]/hook",
		"https://[fd00::1]/hook",
		"https://100.64.0.1/hook",
		"https://0.0.0.0/hook",
		"https://[::]/hook",
		"https://[::ffff:127.0.0.1]/hook",
		"https://[64:ff9b::7f00:1]/hook",
		"https://224.0.0.1/hook",
		"http://agent.example/hook",
		"ftp://agent.example/hook",
		"https://user:pass@agent.example/hook",
		"https:///hook",
		"not a url",
	}
	for _, raw := range refused {
		err := CheckWebhookURL(context.Background(), raw, false, fixedLookup("203.0.113.7"))
		if !errors.Is(err, ErrWebhookURL) {
			t.Errorf("%s: want ErrWebhookURL, got %v", raw, err)
		}
	}
}

func TestCheckWebhookURLRefusesAHostNameThatResolvesInside(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "::1"} {
		err := CheckWebhookURL(context.Background(), "https://rebind.example/hook", false, fixedLookup("203.0.113.7", addr))
		if !errors.Is(err, ErrWebhookURL) {
			t.Errorf("resolves to %s: want ErrWebhookURL, got %v", addr, err)
		}
	}
}

func TestCheckWebhookURLAcceptsAPublicHTTPSHost(t *testing.T) {
	if err := CheckWebhookURL(context.Background(), "https://agent.example/hook", false, fixedLookup("203.0.113.7")); err != nil {
		t.Fatal(err)
	}
	if err := CheckWebhookURL(context.Background(), "https://203.0.113.7:8443/hook", false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCheckWebhookURLOperatorMayAllowPrivate(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:9000/hook", "https://10.0.0.5/hook", "http://runner.internal/hook"} {
		if err := CheckWebhookURL(context.Background(), raw, true, fixedLookup("10.0.0.5")); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	if err := CheckWebhookURL(context.Background(), "https://user:pass@10.0.0.5/hook", true, nil); !errors.Is(err, ErrWebhookURL) {
		t.Fatalf("userinfo must stay refused, got %v", err)
	}
}

func TestWebhookClientRefusesLoopbackAtConnectTime(t *testing.T) {
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit <- struct{}{} }))
	defer srv.Close()
	client := NewWebhookClient(false)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("{}"))
	if res, err := client.Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatal("loopback connection was allowed")
	}
	select {
	case <-hit:
		t.Fatal("server was reached")
	default:
	}
}

func TestWebhookClientRefusesAHostNameThatResolvesToLoopback(t *testing.T) {
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit <- struct{}{} }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	client := newWebhookClient(false, fixedLookup("127.0.0.1"))
	req, _ := http.NewRequest(http.MethodPost, "http://rebind.example:"+port+"/hook", strings.NewReader("{}"))
	if res, err := client.Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatal("rebound name was allowed")
	}
	select {
	case <-hit:
		t.Fatal("server was reached")
	default:
	}
}

func TestWebhookClientAllowPrivateReachesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("{}"))
	res, err := NewWebhookClient(true).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestWebhookClientDoesNotFollowRedirects(t *testing.T) {
	target := make(chan struct{}, 1)
	inner := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { target <- struct{}{} }))
	defer inner.Close()
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inner.URL, http.StatusTemporaryRedirect)
	}))
	defer outer.Close()
	// Allow private so the first hop connects; the redirect must still not be followed.
	req, _ := http.NewRequest(http.MethodPost, outer.URL, strings.NewReader("{}"))
	res, err := NewWebhookClient(true).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status %d", res.StatusCode)
	}
	select {
	case <-target:
		t.Fatal("redirect target was reached")
	default:
	}
}

func TestDeliverOnceDefaultClientRefusesAStoredInternalURL(t *testing.T) {
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit <- struct{}{} }))
	defer srv.Close()
	log := &store.Memory{}
	feed := New(log)
	feed.backoff = func(int) time.Duration { return time.Hour }
	if _, _, err := feed.Subscribe(context.Background(), "ada", "inbox-a", srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := log.Put(context.Background(), "inbox-a", "", "s1", []store.Record{{MessageID: "m1", Kind: "created"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := feed.DeliverOnce(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-hit:
		t.Fatal("default client reached a loopback subscriber")
	default:
	}
	if _, posted := feed.Counters(); posted != 0 {
		t.Fatalf("posted %d", posted)
	}
}
