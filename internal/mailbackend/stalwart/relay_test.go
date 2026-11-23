// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetRelayCreatesTheRouteAndKeepsLocalMail(t *testing.T) {
	var sawPassword string
	var sawElse string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "operator@example.com" || pass != "operator-secret" {
			t.Errorf("basic auth %q %q", user, pass)
		}
		names, args := allCalls(t, r.Body)
		switch names[0] {
		case "x:MtaRoute/query":
			writeMethods(w, []any{
				[]any{"x:MtaRoute/query", map[string]any{"ids": []any{}}, "q"},
				[]any{"x:MtaRoute/get", map[string]any{"list": []any{}}, "g"},
			})
		case "x:MtaRoute/set":
			created := args[0]["create"].(map[string]any)["sm"].(map[string]any)
			if created["host"] != "smtp.example.com" || created["port"] != float64(587) || created["authUsername"] != "mailer" {
				t.Errorf("route %v", created)
			}
			secret := created["authSecret"].(map[string]any)
			sawPassword, _ = secret["secret"].(string)
			strategy := args[1]["update"].(map[string]any)["singleton"].(map[string]any)["route"].(map[string]any)
			sawElse, _ = strategy["else"].(string)
			match := strategy["match"].(map[string]any)["0"].(map[string]any)
			if match["then"] != "'local'" || names[2] != "x:Action/ReloadSettings/set" {
				t.Errorf("strategy %v methods %v", strategy, names)
			}
			writeMethods(w, []any{
				[]any{"x:MtaRoute/set", map[string]any{"created": map[string]any{"sm": map[string]any{"id": "route-1"}}}, "r"},
				[]any{"x:MtaOutboundStrategy/set", map[string]any{"updated": map[string]any{"singleton": nil}}, "o"},
				[]any{"x:Action/ReloadSettings/set", map[string]any{"created": map[string]any{"a": map[string]any{"id": "reload"}}}, "z"},
			})
		default:
			t.Errorf("method %s", names[0])
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	err := SetRelay(context.Background(), &Client{BaseURL: srv.URL, User: "operator@example.com", Password: "operator-secret"}, Relay{
		Host:     "smtp.example.com",
		User:     "mailer",
		Password: "relay-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sawPassword != "relay-secret" || sawElse != "'inbai-relay'" {
		t.Fatalf("password %q else %q", sawPassword, sawElse)
	}
}

func TestSetRelayUpdatesAnExistingRoute(t *testing.T) {
	var updated bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		switch names[0] {
		case "x:MtaRoute/query":
			writeMethods(w, []any{
				[]any{"x:MtaRoute/query", map[string]any{"ids": []any{"route-1"}}, "q"},
				[]any{"x:MtaRoute/get", map[string]any{"list": []any{map[string]any{"id": "route-1", "name": "inbai-relay"}}}, "g"},
			})
		case "x:MtaRoute/set":
			if args[0]["update"].(map[string]any)["route-1"] == nil {
				t.Errorf("update %v", args[0])
			}
			updated = true
			writeMethods(w, []any{
				[]any{"x:MtaRoute/set", map[string]any{"updated": map[string]any{"route-1": nil}}, "r"},
				[]any{"x:MtaOutboundStrategy/set", map[string]any{"updated": map[string]any{"singleton": nil}}, "o"},
				[]any{"x:Action/ReloadSettings/set", map[string]any{"created": map[string]any{"a": nil}}, "z"},
			})
		default:
			t.Errorf("method %s", names[0])
		}
	}))
	defer srv.Close()
	if err := SetRelay(context.Background(), &Client{BaseURL: srv.URL, User: "operator@example.com", Password: "pw"}, Relay{
		Host: "smtp.example.com", Port: 2525, User: "mailer", Password: "secret", ImplicitTLS: false, AllowInvalidCerts: true,
	}); err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("route was not updated")
	}
}

func TestDirectDeliveryRemovesTheRelay(t *testing.T) {
	var sawElse string
	var destroyed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		switch names[0] {
		case "x:MtaRoute/query":
			writeMethods(w, []any{
				[]any{"x:MtaRoute/query", map[string]any{"ids": []any{"route-1"}}, "q"},
				[]any{"x:MtaRoute/get", map[string]any{"list": []any{map[string]any{"id": "route-1", "name": "inbai-relay"}}}, "g"},
			})
		case "x:MtaRoute/set":
			destroyed = args[0]["destroy"].([]any)[0] == "route-1"
			sawElse, _ = args[1]["update"].(map[string]any)["singleton"].(map[string]any)["route"].(map[string]any)["else"].(string)
			writeMethods(w, []any{
				[]any{"x:MtaRoute/set", map[string]any{"destroyed": []string{"route-1"}}, "r"},
				[]any{"x:MtaOutboundStrategy/set", map[string]any{"updated": map[string]any{"singleton": nil}}, "o"},
				[]any{"x:Action/ReloadSettings/set", map[string]any{"created": map[string]any{"a": nil}}, "z"},
			})
		default:
			t.Errorf("method %s", names[0])
		}
	}))
	defer srv.Close()
	if err := DirectDelivery(context.Background(), &Client{BaseURL: srv.URL, User: "operator@example.com", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if !destroyed || sawElse != "'mx'" {
		t.Fatalf("destroyed %v else %q", destroyed, sawElse)
	}
}

func TestSetRelayRejectsAMissingPassword(t *testing.T) {
	err := SetRelay(context.Background(), &Client{}, Relay{Host: "smtp.example.com", User: "mailer"})
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("err = %v", err)
	}
}

func TestRelayBodyIsNotLoggedByTheCaller(t *testing.T) {
	// The secret stays in the JSON body. This guards against putting it on the URL.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "x:MtaRoute/set") && !strings.Contains(string(raw), "relay-secret") {
			t.Errorf("set body missing secret")
		}
		if strings.Contains(r.URL.String(), "relay-secret") {
			t.Errorf("secret in url %s", r.URL.String())
		}
		names, _ := allCalls(t, strings.NewReader(string(raw)))
		if names[0] == "x:MtaRoute/query" {
			writeMethods(w, []any{
				[]any{"x:MtaRoute/query", map[string]any{"ids": []any{}}, "q"},
				[]any{"x:MtaRoute/get", map[string]any{"list": []any{}}, "g"},
			})
			return
		}
		writeMethods(w, []any{
			[]any{"x:MtaRoute/set", map[string]any{"created": map[string]any{"sm": map[string]any{"id": "route-1"}}}, "r"},
			[]any{"x:MtaOutboundStrategy/set", map[string]any{"updated": map[string]any{"singleton": nil}}, "o"},
			[]any{"x:Action/ReloadSettings/set", map[string]any{"created": map[string]any{"a": nil}}, "z"},
		})
	}))
	defer srv.Close()
	if err := SetRelay(context.Background(), &Client{BaseURL: srv.URL, User: "op", Password: "pw"}, Relay{
		Host: "smtp.example.com", User: "mailer", Password: "relay-secret",
	}); err != nil {
		t.Fatal(err)
	}
}
