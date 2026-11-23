// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
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

func TestListDeleteAndDownload(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{
		ID: "m", Attachments: []mailbackend.Attachment{{Name: "note.txt", Type: "text/plain", BlobID: "blob-1"}},
	}}}
	srv := httptest.NewServer((&Server{Service: service.New(mail), Token: "operator-token", Domain: "example.com"}).Handler())
	defer srv.Close()
	res := call(t, srv.URL, http.MethodGet, "/v1/inboxes", "operator-token", "", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(readBody(t, res), `"address":"alice@example.com"`) {
		t.Fatalf("list %d", res.StatusCode)
	}
	res = call(t, srv.URL, http.MethodDelete, "/v1/inboxes/alice-id", "operator-token", "", "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete %d %s", res.StatusCode, readBody(t, res))
	}
	res = call(t, srv.URL, http.MethodGet, "/v1/inboxes/alice-id/messages/m/attachments/blob-1", "operator-token", "", "")
	if res.StatusCode != http.StatusOK || readBody(t, res) != "file-bytes" {
		t.Fatalf("download %d", res.StatusCode)
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

func TestMCPPost(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "m", Subject: "hello", Text: "body"}}}
	srv := httptest.NewServer((&Server{Service: service.New(mail), Token: "operator-token"}).Handler())
	defer srv.Close()
	res := call(t, srv.URL, http.MethodPost, "/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous %d", res.StatusCode)
	}
	res = call(t, srv.URL, http.MethodGet, "/mcp", "operator-token", "", "")
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("get %d", res.StatusCode)
	}
	res = call(t, srv.URL, http.MethodPost, "/mcp", "operator-token", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{"inbox":"inbox-a"}}}`, "")
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "hello") || !strings.Contains(body, "externalContent") {
		t.Fatalf("list %d %s", res.StatusCode, body)
	}
}

func TestListPagesWithACursor(t *testing.T) {
	mail := &fakeMail{messages: []mailbackend.Message{{ID: "a", Subject: "one"}, {ID: "b", Subject: "two"}}}
	srv := httptest.NewServer((&Server{
		Service: service.New(mail),
		Token:   "operator-token",
		Domain:  "example.com",
	}).Handler())
	defer srv.Close()
	res := call(t, srv.URL, http.MethodGet, "/v1/inboxes/alice-id/messages?limit=1", "operator-token", "", "")
	body := readBody(t, res)
	var page struct {
		Messages []mailbackend.Message `json:"messages"`
		Next     string                `json:"next"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || res.StatusCode != http.StatusOK || len(page.Messages) != 1 || page.Messages[0].ID != "a" || page.Next == "" {
		t.Fatalf("first %d %s", res.StatusCode, body)
	}
	res = call(t, srv.URL, http.MethodGet, "/v1/inboxes/alice-id/messages?limit=1&cursor="+page.Next, "operator-token", "", "")
	body = readBody(t, res)
	page = struct {
		Messages []mailbackend.Message `json:"messages"`
		Next     string                `json:"next"`
	}{}
	if err := json.Unmarshal([]byte(body), &page); err != nil || len(page.Messages) != 1 || page.Messages[0].ID != "b" || page.Next != "" {
		t.Fatalf("second %d %s", res.StatusCode, body)
	}
	res = call(t, srv.URL, http.MethodGet, "/v1/inboxes/alice-id/messages?cursor=not-a-cursor", "operator-token", "", "")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad cursor %d", res.StatusCode)
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
	listed   bool
	jmap     string
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

func (f *fakeMail) List(_ context.Context, _ string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	f.listed = true
	return mailbackend.Slice(f.messages, func(m mailbackend.Message) string { return m.ID }, page)
}

func (f *fakeMail) Search(_ context.Context, _, _ string, page mailbackend.Page) ([]mailbackend.Message, string, error) {
	f.listed = true
	return mailbackend.Slice(f.messages, func(m mailbackend.Message) string { return m.ID }, page)
}

func (f *fakeMail) Get(context.Context, string, string) (mailbackend.Message, error) {
	if len(f.messages) == 0 {
		return mailbackend.Message{}, nil
	}
	return f.messages[0], nil
}

func (f *fakeMail) Delete(context.Context, string, string) error { return nil }

func (f *fakeMail) MailState(context.Context, string) (string, error) {
	if f.jmap == "" {
		f.jmap = "s0"
	}
	return f.jmap, nil
}

func (f *fakeMail) MailChanges(_ context.Context, _, since string) (mailbackend.Delta, error) {
	if since == "s0" {
		f.jmap = "s1"
		return mailbackend.Delta{State: "s1", Created: []string{"m-new"}}, nil
	}
	return mailbackend.Delta{State: since}, nil
}

func (f *fakeMail) ListInboxes(context.Context, mailbackend.Page) ([]mailbackend.Account, string, error) {
	f.listed = true
	return []mailbackend.Account{{ID: "alice-id", Local: "alice"}}, "", nil
}

func (f *fakeMail) DeleteInbox(context.Context, string) error { return nil }

func (f *fakeMail) Download(context.Context, string, string) ([]byte, error) {
	return []byte("file-bytes"), nil
}
