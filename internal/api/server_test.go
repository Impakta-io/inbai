// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

func TestCreateListAndSend(t *testing.T) {
	mail := &fakeMail{id: "alice-id", messages: []mailbackend.Message{{ID: "m", Subject: "hello", Preview: "body"}}}
	srv := httptest.NewServer((&Server{
		Service:  service.New(mail),
		Token:    "operator-token",
		Domain:   "example.com",
		DomainID: "dom",
		RoleID:   "role",
	}).Handler())
	defer srv.Close()

	if res := call(t, srv.URL, http.MethodPost, "/v1/inboxes", "nope", `{"localPart":"alice"}`, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", res.StatusCode)
	}
	res := call(t, srv.URL, http.MethodPost, "/v1/inboxes", "operator-token", `{"localPart":"alice"}`, "create-1")
	if res.StatusCode != http.StatusCreated || !strings.Contains(readBody(t, res), `"address":"alice@example.com"`) {
		body := ""
		if res.Body != nil {
			body = readBody(t, res)
		}
		t.Fatalf("create %d %s", res.StatusCode, body)
	}
	res = call(t, srv.URL, http.MethodPost, "/v1/inboxes/alice-id/messages", "operator-token", `{"from":"alice@example.com","to":"bob@example.com","subject":"hello","text":"body"}`, "send-1")
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("send %d %s", res.StatusCode, readBody(t, res))
	}
	res = call(t, srv.URL, http.MethodPost, "/v1/inboxes/alice-id/messages", "operator-token", `{"from":"alice@example.com","to":"bob@example.com","subject":"hello","text":"body"}`, "send-1")
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("replay %d %s", res.StatusCode, readBody(t, res))
	}
	if mail.sent != 1 {
		t.Fatalf("sends %d, want the repeat of the same key to be skipped", mail.sent)
	}
	res = call(t, srv.URL, http.MethodGet, "/v1/inboxes/alice-id/messages", "operator-token", "", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(readBody(t, res), `"subject":"hello"`) {
		t.Fatalf("list %d", res.StatusCode)
	}
}

func TestReadSearchReplyAndDelete(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "hello", Preview: "body", Text: "body"}}}
	srv := httptest.NewServer((&Server{Service: service.New(mail), Token: "operator-token", Domain: "example.com"}).Handler())
	defer srv.Close()
	res := call(t, srv.URL, http.MethodGet, "/v1/inboxes/alice-id/messages/m", "operator-token", "", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(readBody(t, res), `"text":"body"`) {
		t.Fatalf("get %d", res.StatusCode)
	}
	res = call(t, srv.URL, http.MethodGet, "/v1/inboxes/alice-id/messages?q=hello", "operator-token", "", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(readBody(t, res), `"subject":"hello"`) {
		t.Fatalf("search %d", res.StatusCode)
	}
	res = call(t, srv.URL, http.MethodPost, "/v1/inboxes/alice-id/messages/m/reply", "operator-token", `{"from":"alice@example.com","to":"bob@example.com","text":"reply"}`, "")
	if res.StatusCode != http.StatusAccepted || mail.sent != 1 {
		t.Fatalf("reply %d sends %d", res.StatusCode, mail.sent)
	}
	res = call(t, srv.URL, http.MethodDelete, "/v1/inboxes/alice-id/messages/m", "operator-token", "", "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete %d %s", res.StatusCode, readBody(t, res))
	}
}

func TestGenerateAndSendAnAttachment(t *testing.T) {
	mail := &fakeMail{}
	svc := service.New(mail)
	svc.SetSyncLimit(1)
	srv := httptest.NewServer((&Server{
		Service: svc, Token: "operator-token", Domain: "example.com", DomainID: "dom", RoleID: "role",
	}).Handler())
	defer srv.Close()
	res := call(t, srv.URL, http.MethodPost, "/v1/inboxes/generate", "operator-token", `{"prefix":"u","count":1}`, "")
	if res.StatusCode != http.StatusCreated || !strings.Contains(readBody(t, res), "u00000@example.com") {
		t.Fatalf("generate %d", res.StatusCode)
	}
	res = call(t, srv.URL, http.MethodPost, "/v1/inboxes/generate", "operator-token", `{"prefix":"u","count":2}`, "")
	body := readBody(t, res)
	if res.StatusCode != http.StatusAccepted || !strings.Contains(body, `"jobId"`) {
		t.Fatalf("job %d %s", res.StatusCode, body)
	}
	res = call(t, srv.URL, http.MethodPost, "/v1/inboxes/alice-id/messages", "operator-token", `{"from":"alice@example.com","to":"bob@example.com","subject":"file","text":"see","attachments":[{"name":"note.txt","type":"text/plain","data":"bm90ZQ=="}]}`, "file-1")
	if res.StatusCode != http.StatusAccepted || len(mail.last.Attachments) != 1 || string(mail.last.Attachments[0].Data) != "note" {
		t.Fatalf("attach %d %+v", res.StatusCode, mail.last.Attachments)
	}
}

func TestQuotaIsAProblem(t *testing.T) {
	mail := &fakeMail{id: "alice-id"}
	svc := service.New(mail)
	svc.SetQuota(1, 500)
	handler := (&Server{Service: svc, Token: "operator-token", Domain: "example.com"}).Handler()
	body := `{"from":"alice@example.com","to":"bob@example.com","subject":"hi","text":"x"}`
	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/inboxes/alice-id/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer operator-token")
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first %d %s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/inboxes/alice-id/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer operator-token")
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusTooManyRequests || !strings.Contains(second.Body.String(), "send quota exceeded") {
		t.Fatalf("quota %d %s", second.Code, second.Body.String())
	}
}

func call(t *testing.T, base, method, path, token, body, idem string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type fakeMail struct {
	id       string
	messages []mailbackend.Message
	sent     int
	made     []string
	last     mailbackend.Outbound
}

func (f *fakeMail) CreateInbox(context.Context, mailbackend.InboxSpec) (string, error) {
	return f.id, nil
}

func (f *fakeMail) CreateInboxes(_ context.Context, specs []mailbackend.InboxSpec) ([]string, error) {
	ids := make([]string, len(specs))
	for i, spec := range specs {
		ids[i] = spec.Local
	}
	f.made = append(f.made, ids...)
	return ids, nil
}

func (f *fakeMail) Deliver(_ context.Context, msg mailbackend.Outbound) error {
	f.sent++
	f.last = msg
	return nil
}

func (f *fakeMail) List(context.Context, string) ([]mailbackend.Message, error) {
	return f.messages, nil
}

func (f *fakeMail) Search(context.Context, string, string) ([]mailbackend.Message, error) {
	return f.messages, nil
}

func (f *fakeMail) Get(context.Context, string, string) (mailbackend.Message, error) {
	if len(f.messages) == 0 {
		return mailbackend.Message{}, nil
	}
	return f.messages[0], nil
}

func (f *fakeMail) Delete(context.Context, string, string) error { return nil }
