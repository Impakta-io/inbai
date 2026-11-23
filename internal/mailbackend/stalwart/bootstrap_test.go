// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootstrapCreatesDomainRolesAccountsAndKey(t *testing.T) {
	var methods []string
	var roleCreates []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jmap/" || r.Method != http.MethodPost {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "recovery-secret" {
			t.Errorf("basic auth = %q %q ok=%v", user, pass, ok)
		}
		name, args := oneCall(t, r.Body)
		methods = append(methods, name)
		switch name {
		case "x:Domain/set":
			create := args["create"].(map[string]any)["d1"].(map[string]any)
			if create["name"] != "example.com" {
				t.Errorf("domain name %v", create["name"])
			}
			writeMethod(w, name, "d", map[string]any{"created": map[string]any{"d1": map[string]any{"id": "dom"}}})
		case "x:SystemSettings/set":
			update := args["update"].(map[string]any)["singleton"].(map[string]any)
			if update["defaultHostname"] != "mail.example.com" || update["defaultDomainId"] != "dom" {
				t.Errorf("settings %v", update)
			}
			writeMethod(w, name, "s", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:Role/query":
			writeMethod(w, name, "q", map[string]any{"ids": []string{}})
		case "x:Role/set":
			create := args["create"].(map[string]any)
			var body map[string]any
			for _, value := range create {
				body = value.(map[string]any)
			}
			roleCreates = append(roleCreates, body)
			id := "inbox-role"
			if strings.Contains(body["description"].(string), "service") {
				id = "service-role"
			}
			key := "inbox"
			if id == "service-role" {
				key = "svc"
			}
			writeMethod(w, name, "r", map[string]any{"created": map[string]any{key: map[string]any{"id": id}}})
		case "x:Account/set":
			create := args["create"].(map[string]any)
			for _, key := range []string{"operator", "svc"} {
				account := create[key].(map[string]any)
				if account["domainId"] != "dom" {
					t.Errorf("%s domain %v", key, account["domainId"])
				}
			}
			writeMethod(w, name, "a", map[string]any{
				"created": map[string]any{
					"operator": map[string]any{"id": "op"},
					"svc":      map[string]any{"id": "svc-id"},
				},
			})
		case "x:ApiKey/set":
			if args["accountId"] != "svc-id" {
				t.Errorf("api key account %v", args["accountId"])
			}
			writeMethod(w, name, "k", map[string]any{"created": map[string]any{"k1": map[string]any{"id": "key", "secret": "shown-once"}}})
		default:
			t.Errorf("unexpected method %s", name)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	result, err := Bootstrap(context.Background(), &Client{
		BaseURL:  srv.URL,
		User:     "admin",
		Password: "recovery-secret",
	}, Input{
		Domain:           "example.com",
		Hostname:         "mail.example.com",
		OperatorPassword: "operator-secret",
		CreateAPIKey:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DomainID != "dom" || result.ServiceAccountID != "svc-id" || result.OperatorAccountID != "op" || result.APIKey != "shown-once" {
		t.Fatalf("result %+v", result)
	}
	want := []string{
		"x:Domain/set",
		"x:SystemSettings/set",
		"x:Role/query",
		"x:Role/set",
		"x:Role/query",
		"x:Role/set",
		"x:Account/set",
		"x:ApiKey/set",
	}
	if strings.Join(methods, ",") != strings.Join(want, ",") {
		t.Fatalf("methods %v", methods)
	}
	if len(roleCreates) != 2 {
		t.Fatalf("roles %d", len(roleCreates))
	}
	for _, role := range roleCreates {
		perms := role["enabledPermissions"].(map[string]any)
		if perms["emailReceive"] != true || perms["authenticate"] != true {
			t.Errorf("role %v missing inbox permissions", role["description"])
		}
	}
	servicePerms := roleCreates[1]["enabledPermissions"].(map[string]any)
	if servicePerms["impersonate"] != true || servicePerms["sysAccountCreate"] != true {
		t.Errorf("service permissions %v", servicePerms)
	}
	inboxPerms := roleCreates[0]["enabledPermissions"].(map[string]any)
	if _, ok := inboxPerms["impersonate"]; ok {
		t.Error("inbox role can impersonate")
	}
}

func TestBootstrapReusesExistingDomainAndRoles(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, args := oneCall(t, r.Body)
		methods = append(methods, name)
		switch name {
		case "x:Domain/set":
			writeMethod(w, name, "d", map[string]any{
				"notCreated": map[string]any{
					"d1": map[string]any{"type": "alreadyExists", "objectId": map[string]any{"id": "dom"}},
				},
			})
		case "x:SystemSettings/set":
			writeMethod(w, name, "s", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:Role/query":
			writeMethod(w, name, "q", map[string]any{"ids": []string{"inbox-role", "service-role"}})
		case "x:Role/get":
			writeMethod(w, name, "g", map[string]any{
				"list": []map[string]any{
					{"id": "inbox-role", "description": inboxRoleDescription},
					{"id": "service-role", "description": serviceRoleDescription},
				},
			})
			_ = args
		case "x:Account/set":
			writeMethod(w, name, "a", map[string]any{
				"notCreated": map[string]any{
					"operator": map[string]any{"type": "alreadyExists", "objectId": map[string]any{"id": "op"}},
					"svc":      map[string]any{"type": "alreadyExists", "objectId": map[string]any{"id": "svc-id"}},
				},
			})
		case "x:ApiKey/set":
			t.Error("api key created on a rerun that asked not to")
		default:
			t.Errorf("unexpected method %s", name)
		}
	}))
	defer srv.Close()

	result, err := Bootstrap(context.Background(), &Client{BaseURL: srv.URL, User: "admin", Password: "pw"}, Input{
		Domain:           "example.com",
		OperatorPassword: "operator-secret",
		CreateAPIKey:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DomainID != "dom" || result.APIKey != "" || result.Hostname != "mail.example.com" {
		t.Fatalf("result %+v", result)
	}
	for _, method := range methods {
		if method == "x:Role/set" || method == "x:ApiKey/set" {
			t.Fatalf("created new objects: %v", methods)
		}
	}
}

func TestBootstrapRejectsAFailedCreate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMethod(w, "x:Domain/set", "d", map[string]any{
			"notCreated": map[string]any{
				"d1": map[string]any{"type": "invalidProperties", "description": "bad name"},
			},
		})
	}))
	defer srv.Close()

	_, err := Bootstrap(context.Background(), &Client{BaseURL: srv.URL, User: "admin", Password: "pw"}, Input{
		Domain:           "example.com",
		OperatorPassword: "operator-secret",
		CreateAPIKey:     true,
	})
	if err == nil || !strings.Contains(err.Error(), "invalidProperties") {
		t.Fatalf("err = %v", err)
	}
}

func oneCall(t *testing.T, body io.Reader) (string, map[string]any) {
	t.Helper()
	var req struct {
		MethodCalls [][]json.RawMessage `json:"methodCalls"`
	}
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		t.Fatal(err)
	}
	if len(req.MethodCalls) != 1 || len(req.MethodCalls[0]) < 2 {
		t.Fatalf("calls %+v", req.MethodCalls)
	}
	var name string
	if err := json.Unmarshal(req.MethodCalls[0][0], &name); err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	if err := json.Unmarshal(req.MethodCalls[0][1], &args); err != nil {
		t.Fatal(err)
	}
	return name, args
}

func writeMethod(w http.ResponseWriter, name, id string, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"methodResponses": []any{[]any{name, payload, id}},
	})
}
