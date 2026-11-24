// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package stalwart

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

func TestGetSearchAndDelete(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		methods = append(methods, names...)
		switch names[0] {
		case "Email/get":
			props, _ := args[0]["properties"].([]any)
			for _, prop := range props {
				if prop == "bodyValues" {
					if _, ok := args[0]["fetchTextBodyValues"]; !ok {
						t.Errorf("get did not ask for body values")
					}
				}
			}
			full := false
			sawAuth := false
			sawFrom := false
			for _, prop := range props {
				if prop == "bodyValues" {
					full = true
				}
				if prop == "headers" {
					sawAuth = true
				}
				if prop == "from" {
					sawFrom = true
				}
			}
			if full && !sawAuth {
				t.Error("get did not ask for authentication results")
			}
			if full && !sawFrom {
				t.Error("get did not ask for the sender")
			}
			writeMethods(w, []any{[]any{"Email/get", map[string]any{"list": []any{map[string]any{
				"id": "msg", "subject": "hello", "preview": "body",
				"messageId":  []string{"<a@example.com>"},
				"textBody":   []any{map[string]any{"partId": "p"}},
				"bodyValues": map[string]any{"p": map[string]any{"value": "body"}},
				"from":       []any{map[string]any{"email": "ada@example.com"}},
				"keywords":   map[string]bool{"$junk": true},
				"headers": []any{
					map[string]any{"name": "Received", "value": " from mx.sender.example by mail.example.com (Stalwart SMTP) with ESMTP id 1;\r\n\tTue, 24 Nov 2026 12:00:00 +0000"},
					map[string]any{"name": "Authentication-Results", "value": " mail.example.com; spf=pass; dkim=pass; dmarc=fail"},
					map[string]any{"name": "From", "value": " ada@example.com"},
				},
			}}}, "g"}})
		case "Mailbox/query":
			writeMethods(w, []any{[]any{"Mailbox/query", map[string]any{"ids": []string{"inbox-box"}}, "q"}})
		case "Email/query":
			filter, _ := args[0]["filter"].(map[string]any)
			if filter["inMailbox"] != "inbox-box" || filter["text"] != "hello" {
				t.Errorf("search filter %#v", filter)
			}
			writeMethods(w, []any{[]any{"Email/query", map[string]any{"ids": []string{"msg"}}, "q"}})
		case "Email/set":
			destroy, _ := args[0]["destroy"].([]any)
			if len(destroy) != 1 || destroy[0] != "msg" {
				t.Errorf("destroy %#v", args[0]["destroy"])
			}
			writeMethods(w, []any{[]any{"Email/set", map[string]any{"destroyed": []string{"msg"}}, "e"}})
		default:
			t.Errorf("method %s", names[0])
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, APIKey: "key"}
	got, err := GetMessage(context.Background(), client, "alice-id", "msg", "mail.example.com")
	if err != nil || got.Text != "body" || got.From != "ada@example.com" || len(got.MessageID) != 1 || got.Authentication.SPF != "pass" || got.Authentication.DMARC != "fail" || got.Authentication.Spam != "spam" {
		t.Fatalf("get %+v %v", got, err)
	}
	found, err := SearchMessages(context.Background(), client, "alice-id", "hello")
	if err != nil || len(found) != 1 {
		t.Fatalf("search %+v %v", found, err)
	}
	if err := DeleteMessage(context.Background(), client, "alice-id", "msg"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "Email/get,Mailbox/query,Email/query,Email/get,Email/set" {
		t.Fatalf("methods %v", methods)
	}
}

// authResultsServer answers Email/get as RFC 8621 section 4.1.3 says Stalwart
// does for a message whose header is block: headers is every field in order,
// with the raw value after the colon, and
// header:Authentication-Results:asText:all is the values of those fields.
func authResultsServer(t *testing.T, block string) *httptest.Server {
	t.Helper()
	type emailHeader struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	var fields []emailHeader
	if block != "" {
		for _, line := range strings.Split(strings.TrimSuffix(block, "\r\n"), "\r\n") {
			if line[0] == ' ' || line[0] == '\t' {
				fields[len(fields)-1].Value += "\r\n" + line
				continue
			}
			name, value, _ := strings.Cut(line, ":")
			fields = append(fields, emailHeader{Name: name, Value: value})
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, args := allCalls(t, r.Body)
		item := map[string]any{"id": "msg", "subject": "hello"}
		props, _ := args[0]["properties"].([]any)
		for _, prop := range props {
			switch prop {
			case "headers":
				item["headers"] = fields
			case "header:Authentication-Results:asText:all":
				texts := []string{}
				for _, field := range fields {
					if strings.EqualFold(field.Name, "Authentication-Results") {
						texts = append(texts, strings.TrimSpace(field.Value))
					}
				}
				item["header:Authentication-Results:asText:all"] = texts
			}
		}
		writeMethods(w, []any{[]any{"Email/get", map[string]any{"list": []any{item}}, "g"}})
	}))
}

// Header blocks as stalwartlabs/stalwart:v1 stored them, anonymised and cut.
const (
	ceoFields = "From: CEO <ceo@outside.example.test>\r\n" +
		"To: alice@example.com\r\n" +
		"Subject: quarterly numbers\r\n" +
		"Date: Tue, 24 Nov 2026 12:00:00 +0000\r\n"
	forgedField = "Authentication-Results: mail.example.com; spf=pass smtp.mailfrom=outside.example.test; dkim=pass header.d=outside.example.test; dmarc=pass header.from=outside.example.test\r\n"

	// Inbound SMTP: Stalwart's Received and Authentication-Results lead.
	inboundBlock = "Delivered-To: alice@example.com\r\n" +
		"X-Spam-Status: No\r\n" +
		"Received: from mx.bank.example (mx.bank.example [192.0.2.1])\r\n" +
		"\tby mail.example.com (Stalwart SMTP) with ESMTPS id 4C0599E33A00001;\r\n" +
		"\tTue, 24 Nov 2026 15:55:42 +0000\r\n" +
		"Authentication-Results: mail.example.com;\r\n" +
		"\tspf=fail smtp.mailfrom=bank.example;\r\n" +
		"\tdkim=fail header.d=bank.example;\r\n" +
		"\tdmarc=fail header.from=bank.example policy.dmarc=reject\r\n" +
		"Received-SPF: fail (mail.example.com: domain of bank.example does not designate 192.0.2.1)\r\n" +
		"X-Spam-Score: spam, score=11.20\r\n" +
		"Return-Path: <help@bank.example>\r\n"

	// Authenticated submission: Stalwart signs the message and adds no trace
	// fields. The forged field below From is what a live test sent.
	submissionBlock = "Delivered-To: alice@example.com\r\n" +
		"X-Spam-Status: No\r\n" +
		"DKIM-Signature: v=1; a=ed25519-sha256; s=v1-ed25519-20261124; d=example.com; c=relaxed/relaxed; r=y;\r\n" +
		"\th=Date:Message-ID:Subject:To:From; t=1795535801; bh=u5j8L8MB6c95fNAGW1gDwrJ\r\n" +
		"\tfntk4hoUsTG98ojM00ec=; b=rgIcAugvPpU7GGXFjyVkG6Y5eA==;\r\n" +
		ceoFields + forgedField
)

func TestGetReportsOnlyOurServersAuthenticationResults(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  mailbackend.Authentication
	}{
		{"inbound, forged below From", inboundBlock + ceoFields + forgedField, mailbackend.Authentication{SPF: "fail", DKIM: "fail", DMARC: "fail"}},
		{"inbound, forged first line", inboundBlock + forgedField + ceoFields, mailbackend.Authentication{SPF: "fail", DKIM: "fail", DMARC: "fail"}},
		{"submission", submissionBlock, mailbackend.Authentication{}},
		{"forged by another server name", strings.Replace(inboundBlock, "Authentication-Results: mail.example.com;", "Authentication-Results: mx.example;", 1) + forgedField, mailbackend.Authentication{}},
		{"look-alike name", strings.Replace(inboundBlock, "Authentication-Results: mail.example.com;", "Authentication-Results: mail.example.com.evil;", 1), mailbackend.Authentication{}},
		{"none", "", mailbackend.Authentication{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := authResultsServer(t, tc.block)
			defer srv.Close()
			mail := Mail{Client: &Client{BaseURL: srv.URL, APIKey: "key"}, AuthservID: "MAIL.example.com"}
			got, err := mail.Get(context.Background(), "alice-id", "msg")
			if err != nil {
				t.Fatal(err)
			}
			if got.Authentication != tc.want {
				t.Fatalf("authentication %+v, want %+v", got.Authentication, tc.want)
			}
		})
	}
	srv := authResultsServer(t, inboundBlock)
	defer srv.Close()
	got, err := Mail{Client: &Client{BaseURL: srv.URL, APIKey: "key"}}.Get(context.Background(), "alice-id", "msg")
	if err != nil || got.Authentication != (mailbackend.Authentication{}) {
		t.Fatalf("no authserv-id: %+v %v", got.Authentication, err)
	}
}

func TestReplySetsThreadingHeaders(t *testing.T) {
	var sawReply bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		names, args := allCalls(t, strings.NewReader(string(body)))
		switch names[0] {
		case "Identity/get":
			writeMethods(w, []any{[]any{"Identity/get", map[string]any{"list": []any{map[string]any{"id": "ident", "email": "alice@example.com"}}}, "i"}})
		case "Mailbox/get":
			writeMethods(w, []any{[]any{"Mailbox/get", map[string]any{"list": []any{
				map[string]any{"id": "inbox-box", "role": "inbox"},
				map[string]any{"id": "drafts-box", "role": "drafts"},
				map[string]any{"id": "sent-box", "role": "sent"},
			}}, "m"}})
		case "Email/set":
			create := args[0]["create"].(map[string]any)["m"].(map[string]any)
			reply, _ := create["inReplyTo"].([]any)
			if len(reply) != 1 || reply[0] != "<a@example.com>" {
				t.Errorf("inReplyTo %#v", create["inReplyTo"])
			}
			sawReply = true
			writeMethods(w, []any{
				[]any{"Email/set", map[string]any{"created": map[string]any{"m": map[string]any{"id": "msg"}}}, "e"},
				[]any{"EmailSubmission/set", map[string]any{"created": map[string]any{"s": map[string]any{"id": "sub"}}}, "s"},
			})
		default:
			t.Errorf("%s", names[0])
		}
	}))
	defer srv.Close()
	err := Deliver(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, Delivery{
		AccountID: "alice-id", From: "alice@example.com", To: "bob@example.com",
		Subject: "Re: hello", Body: "reply", InReplyTo: []string{"<a@example.com>"},
	})
	if err != nil || !sawReply {
		t.Fatalf("deliver %v saw %v", err, sawReply)
	}
}

func TestDeleteMessageReportsARefusal(t *testing.T) {
	cases := map[string]struct {
		result  map[string]any
		want    string
		refused string
	}{
		"refused": {
			result:  map[string]any{"notDestroyed": map[string]any{"msg": map[string]any{"type": "forbidden", "description": "Read-only mailbox"}}},
			want:    "stalwart msg: forbidden: Read-only mailbox",
			refused: "forbidden",
		},
		"no result": {
			result: map[string]any{},
			want:   "missing destroy",
		},
		"already gone": {
			result: map[string]any{"notDestroyed": map[string]any{"msg": map[string]any{"type": "notFound"}}},
		},
		"destroyed": {
			result: map[string]any{"destroyed": []string{"msg"}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				names, _ := allCalls(t, r.Body)
				if names[0] != "Email/set" {
					t.Errorf("method %s", names[0])
				}
				writeMethods(w, []any{[]any{"Email/set", tc.result, "e"}})
			}))
			defer srv.Close()
			err := DeleteMessage(context.Background(), &Client{BaseURL: srv.URL, APIKey: "key"}, "alice-id", "msg")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("delete %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("delete error %v, want %q", err, tc.want)
			}
			var refused *mailbackend.RefusedError
			if got := errors.As(err, &refused); got != (tc.refused != "") || (got && (refused.Type != tc.refused || refused.Description == "")) {
				t.Fatalf("delete error %v refusal %+v, want type %q", err, refused, tc.refused)
			}
		})
	}
}

func TestQueryMessagesSortsNewestFirstAndAnchorsAfterTheCursor(t *testing.T) {
	var query map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, args := allCalls(t, r.Body)
		switch names[0] {
		case "Mailbox/query":
			writeMethods(w, []any{[]any{"Mailbox/query", map[string]any{"ids": []string{"inbox-box"}}, "q"}})
		case "Email/query":
			query = args[0]
			writeMethods(w, []any{[]any{"Email/query", map[string]any{"ids": []string{"c", "b", "a"}}, "q"}})
		case "Email/get":
			writeMethods(w, []any{[]any{"Email/get", map[string]any{"list": []any{}}, "g"}})
		}
	}))
	defer srv.Close()
	client := &Client{BaseURL: srv.URL, APIKey: "key"}
	_, next, err := queryMessages(context.Background(), client, "alice-id", nil, mailbackend.Page{After: "d", Limit: 2}, false)
	if err != nil || next != "b" {
		t.Fatalf("next %q err %v", next, err)
	}
	sorts, _ := query["sort"].([]any)
	if len(sorts) != 1 {
		t.Fatalf("sort %#v", query["sort"])
	}
	first, _ := sorts[0].(map[string]any)
	if first["property"] != "receivedAt" || first["isAscending"] != false {
		t.Fatalf("sort %#v", first)
	}
	if query["anchor"] != "d" || query["anchorOffset"] != float64(1) || query["limit"] != float64(3) {
		t.Fatalf("paging %#v", query)
	}
}
