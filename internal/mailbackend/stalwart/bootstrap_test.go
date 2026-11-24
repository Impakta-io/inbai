// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"encoding/json"
	"errors"
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
		case "x:MtaStageData/set":
			update := args["update"].(map[string]any)["singleton"].(map[string]any)
			want := `{"addAuthResultsHeader":{"else":"true","match":{}}}`
			if got, _ := json.Marshal(update); string(got) != want {
				t.Errorf("mta stage data %s, want %s", got, want)
			}
			writeMethod(w, name, "m", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:Role/query":
			writeMethod(w, name, "q", map[string]any{"ids": []string{}})
		case "x:Role/set":
			create := args["create"].(map[string]any)
			var key string
			var body map[string]any
			for createKey, value := range create {
				key = createKey
				body = value.(map[string]any)
			}
			roleCreates = append(roleCreates, body)
			id := "inbox-role"
			switch body["description"].(string) {
			case serviceRoleDescription:
				id = "service-role"
			case operatorRoleDescription:
				id = "operator-role"
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
			operatorRoles := create["operator"].(map[string]any)["roles"].(map[string]any)["roleIds"].(map[string]any)
			serviceRoles := create["svc"].(map[string]any)["roles"].(map[string]any)["roleIds"].(map[string]any)
			if operatorRoles["operator-role"] != true || serviceRoles["service-role"] != true || serviceRoles["operator-role"] == true {
				t.Errorf("operator roles %v service roles %v", operatorRoles, serviceRoles)
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
		"x:MtaStageData/set",
		"x:Role/query",
		"x:Role/set",
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
	if len(roleCreates) != 3 {
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
	if _, ok := servicePerms["sysMtaRouteCreate"]; ok {
		t.Error("service role can change outbound routes")
	}
	operatorPerms := roleCreates[2]["enabledPermissions"].(map[string]any)
	if operatorPerms["sysMtaRouteCreate"] != true || operatorPerms["sysMtaOutboundStrategyUpdate"] != true {
		t.Errorf("operator permissions %v", operatorPerms)
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
		case "x:MtaStageData/set":
			writeMethod(w, name, "m", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:Role/query":
			writeMethod(w, name, "q", map[string]any{"ids": []string{"inbox-role", "service-role"}})
		case "x:Role/get":
			writeMethod(w, name, "g", map[string]any{"list": currentRoles()})
		case "x:Account/set":
			if _, update := args["update"]; update {
				operator := args["update"].(map[string]any)["op"].(map[string]any)
				roles := operator["roles"].(map[string]any)["roleIds"].(map[string]any)
				if roles["operator-role"] != true {
					t.Errorf("rerun roles %v", roles)
				}
				if _, ok := operator["credentials"]; ok {
					t.Error("rerun changed the operator password without being asked")
				}
				writeMethod(w, name, "u", map[string]any{"updated": map[string]any{"op": nil}})
				return
			}
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

// currentRoles is a Role/get list of the three inbAI roles that already hold
// every permission this version grants.
func currentRoles() []map[string]any {
	return []map[string]any{
		{"id": "inbox-role", "description": inboxRoleDescription, "enabledPermissions": permSet(inboxPermissions)},
		{"id": "service-role", "description": serviceRoleDescription, "enabledPermissions": permSet(servicePermissions)},
		{"id": "operator-role", "description": operatorRoleDescription, "enabledPermissions": permSet(operatorPermissions)},
	}
}

func without(names []string, drop ...string) map[string]bool {
	out := permSet(names)
	for _, name := range drop {
		delete(out, name)
	}
	return out
}

func TestRolesGrantDeletePermissions(t *testing.T) {
	inbox, service, operator := permSet(inboxPermissions), permSet(servicePermissions), permSet(operatorPermissions)
	if !inbox["jmapEmailDestroy"] || !service["jmapEmailDestroy"] || !operator["jmapEmailDestroy"] {
		t.Error("a role cannot destroy email, so message delete and retention fail")
	}
	if !service["sysAccountDestroy"] || !operator["sysAccountDestroy"] {
		t.Error("the service role cannot destroy accounts, so inbox delete fails")
	}
	if inbox["sysAccountDestroy"] {
		t.Error("the inbox role can destroy accounts")
	}
}

func TestBootstrapGrantsMissingPermissionsToExistingRoles(t *testing.T) {
	updates := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, args := oneCall(t, r.Body)
		switch name {
		case "x:Domain/set":
			writeMethod(w, name, "d", map[string]any{"notCreated": map[string]any{
				"d1": map[string]any{"type": "alreadyExists", "objectId": map[string]any{"id": "dom"}},
			}})
		case "x:SystemSettings/set":
			writeMethod(w, name, "s", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:MtaStageData/set":
			writeMethod(w, name, "m", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:Role/query":
			writeMethod(w, name, "q", map[string]any{"ids": []string{"inbox-role", "service-role", "operator-role"}})
		case "x:Role/get":
			// An install from before this change: no destroy permissions, and
			// one permission an operator added to the inbox role by hand.
			oldInbox := without(inboxPermissions, "jmapEmailDestroy")
			oldInbox["jmapVacationResponseGet"] = true
			writeMethod(w, name, "g", map[string]any{"list": []map[string]any{
				{"id": "inbox-role", "description": inboxRoleDescription, "enabledPermissions": oldInbox},
				{"id": "service-role", "description": serviceRoleDescription, "enabledPermissions": without(servicePermissions, "jmapEmailDestroy", "sysAccountDestroy")},
				{"id": "operator-role", "description": operatorRoleDescription, "enabledPermissions": permSet(operatorPermissions)},
			}})
		case "x:Role/set":
			if _, ok := args["create"]; ok {
				t.Errorf("created a role that exists: %v", args)
			}
			update, _ := args["update"].(map[string]any)
			for id, patch := range update {
				updates[id] = patch.(map[string]any)
				writeMethod(w, name, "p", map[string]any{"updated": map[string]any{id: nil}})
			}
		case "x:Account/set":
			if _, ok := args["update"]; ok {
				writeMethod(w, name, "u", map[string]any{"updated": map[string]any{"op": nil}})
				return
			}
			writeMethod(w, name, "a", map[string]any{"notCreated": map[string]any{
				"operator": map[string]any{"type": "alreadyExists", "objectId": map[string]any{"id": "op"}},
				"svc":      map[string]any{"type": "alreadyExists", "objectId": map[string]any{"id": "svc-id"}},
			}})
		default:
			t.Errorf("unexpected method %s", name)
		}
	}))
	defer srv.Close()

	result, err := Bootstrap(context.Background(), &Client{BaseURL: srv.URL, User: "admin", Password: "pw"}, Input{
		Domain:           "example.com",
		OperatorPassword: "operator-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.InboxRoleID != "inbox-role" || result.ServiceRoleID != "service-role" {
		t.Fatalf("result %+v", result)
	}
	if len(updates) != 2 || updates["operator-role"] != nil {
		t.Fatalf("updates %v, want the inbox and service roles only", updates)
	}
	for id, want := range map[string][]string{"inbox-role": inboxPermissions, "service-role": servicePermissions} {
		if len(updates[id]) != 1 {
			t.Errorf("%s patch %v changes more than enabledPermissions", id, updates[id])
		}
		perms, _ := updates[id]["enabledPermissions"].(map[string]any)
		for _, name := range want {
			if perms[name] != true {
				t.Errorf("%s update lacks %s", id, name)
			}
		}
	}
	inboxPerms := updates["inbox-role"]["enabledPermissions"].(map[string]any)
	if inboxPerms["jmapVacationResponseGet"] != true {
		t.Error("the update dropped a permission the operator added")
	}
	if len(inboxPerms) != len(inboxPermissions)+1 {
		t.Errorf("inbox role has %d permissions, want %d", len(inboxPerms), len(inboxPermissions)+1)
	}
}

func TestBootstrapReportsARefusedRoleUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, _ := oneCall(t, r.Body)
		switch name {
		case "x:Domain/set":
			writeMethod(w, name, "d", map[string]any{"created": map[string]any{"d1": map[string]any{"id": "dom"}}})
		case "x:SystemSettings/set":
			writeMethod(w, name, "s", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:MtaStageData/set":
			writeMethod(w, name, "m", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:Role/query":
			writeMethod(w, name, "q", map[string]any{"ids": []string{"inbox-role"}})
		case "x:Role/get":
			writeMethod(w, name, "g", map[string]any{"list": []map[string]any{
				{"id": "inbox-role", "description": inboxRoleDescription, "enabledPermissions": without(inboxPermissions, "jmapEmailDestroy")},
			}})
		case "x:Role/set":
			writeMethod(w, name, "p", map[string]any{"notUpdated": map[string]any{
				"inbox-role": map[string]any{"type": "invalidProperties", "description": "unknown permission"},
			}})
		default:
			t.Errorf("unexpected method %s", name)
		}
	}))
	defer srv.Close()

	_, err := Bootstrap(context.Background(), &Client{BaseURL: srv.URL, User: "admin", Password: "pw"}, Input{
		Domain:           "example.com",
		OperatorPassword: "operator-secret",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown permission") {
		t.Fatalf("err = %v", err)
	}
}

func TestBootstrapResetsAnExistingOperatorPassword(t *testing.T) {
	var update map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, args := oneCall(t, r.Body)
		switch name {
		case "x:Domain/set":
			writeMethod(w, name, "d", map[string]any{"created": map[string]any{"d1": map[string]any{"id": "dom"}}})
		case "x:SystemSettings/set":
			writeMethod(w, name, "s", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:MtaStageData/set":
			writeMethod(w, name, "m", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:Role/query":
			writeMethod(w, name, "q", map[string]any{"ids": []string{"r"}})
		case "x:Role/get":
			writeMethod(w, name, "g", map[string]any{"list": currentRoles()})
		case "x:Account/set":
			if raw, ok := args["update"]; ok {
				update = raw.(map[string]any)["op"].(map[string]any)
				writeMethod(w, name, "u", map[string]any{"updated": map[string]any{"op": nil}})
				return
			}
			writeMethod(w, name, "a", map[string]any{"notCreated": map[string]any{
				"operator": map[string]any{"type": "alreadyExists", "objectId": map[string]any{"id": "op"}},
				"svc":      map[string]any{"type": "alreadyExists", "objectId": map[string]any{"id": "svc-id"}},
			}})
		default:
			t.Errorf("unexpected method %s", name)
		}
	}))
	defer srv.Close()

	_, err := Bootstrap(context.Background(), &Client{BaseURL: srv.URL, User: "admin", Password: "pw"}, Input{
		Domain:                "example.com",
		OperatorPassword:      "new-operator-secret",
		ResetOperatorPassword: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	creds, _ := update["credentials"].(map[string]any)
	first, _ := creds["0"].(map[string]any)
	if first["@type"] != "Password" || first["secret"] != "new-operator-secret" || len(creds) != 1 {
		t.Fatalf("operator update %v", update)
	}
	roles := update["roles"].(map[string]any)["roleIds"].(map[string]any)
	if roles["operator-role"] != true {
		t.Fatalf("roles %v", roles)
	}
}

func TestCheckAPIKeyTellsRejectedFromUnchecked(t *testing.T) {
	for _, tc := range []struct {
		status   int
		ok       bool
		rejected bool
	}{
		{http.StatusOK, true, false},
		{http.StatusUnauthorized, false, true},
		{http.StatusForbidden, false, true},
		{http.StatusInternalServerError, false, false},
		{http.StatusServiceUnavailable, false, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/jmap/" || r.Header.Get("Authorization") != "Bearer the-key" {
				t.Errorf("request %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
			}
			if name, _ := oneCall(t, r.Body); name != "Core/echo" {
				t.Errorf("method %s", name)
			}
			w.WriteHeader(tc.status)
		}))
		err := CheckAPIKey(context.Background(), &Client{BaseURL: srv.URL, APIKey: "the-key"})
		srv.Close()
		if (err == nil) != tc.ok || errors.Is(err, ErrAPIKeyRejected) != tc.rejected {
			t.Errorf("HTTP %d: err %v", tc.status, err)
		}
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	err := CheckAPIKey(context.Background(), &Client{BaseURL: url, APIKey: "the-key"})
	if err == nil || errors.Is(err, ErrAPIKeyRejected) {
		t.Fatalf("unreachable server: err %v", err)
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

func TestBootstrapStopsWhenTheAuthResultsSettingIsRefused(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, _ := oneCall(t, r.Body)
		methods = append(methods, name)
		switch name {
		case "x:Domain/set":
			writeMethod(w, name, "d", map[string]any{"created": map[string]any{"d1": map[string]any{"id": "dom"}}})
		case "x:SystemSettings/set":
			writeMethod(w, name, "s", map[string]any{"updated": map[string]any{"singleton": nil}})
		case "x:MtaStageData/set":
			writeMethod(w, name, "m", map[string]any{"notUpdated": map[string]any{
				"singleton": map[string]any{"type": "invalidProperties", "description": "addAuthResultsHeader"},
			}})
		default:
			t.Errorf("went on to %s after the setting was refused", name)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	_, err := Bootstrap(context.Background(), &Client{BaseURL: srv.URL, User: "admin", Password: "pw"}, Input{
		Domain:           "example.com",
		OperatorPassword: "operator-secret",
	})
	if err == nil || !strings.Contains(err.Error(), "addAuthResultsHeader") {
		t.Fatalf("err = %v, methods %v", err, methods)
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
