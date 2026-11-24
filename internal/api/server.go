// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/mcp"
	"github.com/Impakta-io/inbai/internal/service"
)

// Server is the REST transport. It authenticates the caller and calls the service layer.
type Server struct {
	Service       *service.Service
	Directory     *auth.Directory
	Token         string
	Issuer        string
	Domain        string
	DomainID      string
	RoleID        string
	IngestSecret  string
	RetentionDays int
	Log           io.Writer
}

type callerKey struct{}

// Handler returns the HTTP routes under /v1.
func (s *Server) Handler() http.Handler {
	public := http.NewServeMux()
	public.HandleFunc("POST /oauth/token", s.oauthToken)
	public.HandleFunc("GET /.well-known/oauth-authorization-server", s.authorizationMetadata)
	public.HandleFunc("GET /.well-known/oauth-protected-resource", s.resourceMetadata)
	public.HandleFunc("GET /healthz", s.health)
	public.HandleFunc("GET /readyz", s.ready)
	public.HandleFunc("GET /metrics", s.metrics)
	public.HandleFunc("POST /v1/hooks/ingest", s.ingest)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", s.mcp)
	mux.HandleFunc("GET /mcp", s.mcp)
	mux.HandleFunc("GET /v1/events", s.events)
	mux.HandleFunc("GET /v1/suppressions", s.suppressions)
	mux.HandleFunc("POST /v1/webhooks", s.createWebhook)
	mux.HandleFunc("POST /v1/agents", s.createAgent)
	mux.HandleFunc("POST /v1/agents/{name}/suspend", s.suspendAgent)
	mux.HandleFunc("POST /v1/grants", s.createGrant)
	mux.HandleFunc("DELETE /v1/grants/{agent}/{inbox}/{permission}", s.revokeGrant)
	mux.HandleFunc("GET /v1/audit", s.audit)
	mux.HandleFunc("GET /v1/retention", s.getRetention)
	mux.HandleFunc("PUT /v1/retention", s.putRetention)
	mux.HandleFunc("POST /v1/retention/run", s.runRetention)
	mux.HandleFunc("POST /v1/inboxes", s.createInbox)
	mux.HandleFunc("GET /v1/inboxes", s.listInboxes)
	mux.HandleFunc("DELETE /v1/inboxes/{id}", s.deleteInbox)
	mux.HandleFunc("GET /v1/inboxes/{id}/allowlist", s.getAllowlist)
	mux.HandleFunc("PUT /v1/inboxes/{id}/allowlist", s.putAllowlist)
	mux.HandleFunc("GET /v1/inboxes/{id}/quota", s.getQuota)
	mux.HandleFunc("PUT /v1/inboxes/{id}/quota", s.putQuota)
	mux.HandleFunc("POST /v1/inboxes/generate", s.generateInboxes)
	mux.HandleFunc("GET /v1/jobs/{id}", s.getJob)
	mux.HandleFunc("GET /v1/inboxes/{id}/messages", s.listMessages)
	mux.HandleFunc("POST /v1/inboxes/{id}/messages", s.sendMessage)
	mux.HandleFunc("GET /v1/inboxes/{id}/messages/{messageId}", s.getMessage)
	mux.HandleFunc("GET /v1/inboxes/{id}/messages/{messageId}/attachments/{blobId}", s.attachment)
	mux.HandleFunc("DELETE /v1/inboxes/{id}/messages/{messageId}", s.deleteMessage)
	mux.HandleFunc("POST /v1/inboxes/{id}/messages/{messageId}/reply", s.replyMessage)
	return s.logged(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" || r.URL.Path == "/v1/hooks/ingest" || strings.HasPrefix(r.URL.Path, "/.well-known/") {
			public.ServeHTTP(w, r)
			return
		}
		caller, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	}))
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	mcp.ServeHTTP(w, r, mcp.ServiceBox{
		Service: s.Service,
		Caller:  s.requestCaller(r),
		Public:  func(err error) error { return toolError(w, err) },
	})
}

func (s *Server) requestCaller(r *http.Request) service.Caller {
	caller, _ := r.Context().Value(callerKey{}).(service.Caller)
	return caller
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (service.Caller, bool) {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || got == "" {
		s.writeUnauthorized(w)
		return service.Caller{}, false
	}
	if s.Token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1 {
		return service.Caller{Operator: true}, true
	}
	if s.Directory == nil {
		s.writeUnauthorized(w)
		return service.Caller{}, false
	}
	subject, operator, err := s.Directory.Verify(got)
	if err != nil {
		s.writeUnauthorized(w)
		return service.Caller{}, false
	}
	if operator {
		return service.Caller{Operator: true}, true
	}
	return service.Caller{Agent: subject}, true
}

func (s *Server) writeUnauthorized(w http.ResponseWriter) {
	if s.Issuer != "" {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+s.Issuer+`/.well-known/oauth-protected-resource"`)
	}
	writeProblem(w, http.StatusUnauthorized, "Unauthorized", "bearer token is missing or rejected")
}

// tokenBodyLimit bounds the /oauth/token form. A client_credentials request is
// a grant type, a client id, and a secret. The route is read before the caller
// is authenticated.
const tokenBodyLimit = 4 << 10

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if s.Directory == nil {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, tokenBodyLimit)
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
			return
		}
		http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id = r.PostForm.Get("client_id")
		secret = r.PostForm.Get("client_secret")
	}
	if r.PostForm.Get("grant_type") != "client_credentials" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unsupported_grant_type"}`))
		return
	}
	token, expires, err := s.Directory.Exchange(id, secret)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "bearer",
		"expires_in":   expires,
	})
}

func (s *Server) authorizationMetadata(w http.ResponseWriter, r *http.Request) {
	issuer := s.issuer()
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                issuer,
		"token_endpoint":                        issuer + "/oauth/token",
		"grant_types_supported":                 []string{"client_credentials"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"response_types_supported":              []string{},
	})
}

func (s *Server) resourceMetadata(w http.ResponseWriter, r *http.Request) {
	issuer := s.issuer()
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 issuer,
		"authorization_servers":    []string{issuer},
		"bearer_methods_supported": []string{"header"},
	})
}

func (s *Server) issuer() string {
	return strings.TrimRight(s.Issuer, "/")
}

func (s *Server) identities() service.Identities {
	if s.Directory == nil {
		return nil
	}
	return s.Directory
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	id, secret, err := s.Service.ProvisionAgent(s.requestCaller(r), s.identities(), body.Name)
	if err != nil && secret == "" {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "secret": secret, "agent": body.Name})
}

func (s *Server) suspendAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.Service.SuspendAgent(s.requestCaller(r), s.identities(), r.PathValue("name")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createGrant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent      string `json:"agent"`
		Inbox      string `json:"inbox"`
		Permission string `json:"permission"`
	}
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	err := s.Service.Grant(s.requestCaller(r), body.Agent, body.Inbox, service.Permission(body.Permission))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"agent": body.Agent, "inbox": body.Inbox, "permission": body.Permission})
}

func (s *Server) revokeGrant(w http.ResponseWriter, r *http.Request) {
	err := s.Service.Revoke(s.requestCaller(r), r.PathValue("agent"), r.PathValue("inbox"), service.Permission(r.PathValue("permission")))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	var after int64
	if raw := r.URL.Query().Get("after"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "after must be an integer")
			return
		}
		after = value
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > mailbackend.MaxLimit {
			writeProblem(w, http.StatusBadRequest, "Bad request", "limit must be from 1 to 100")
			return
		}
		limit = value
	}
	rows, next, err := s.Service.Audit(r.Context(), s.requestCaller(r), after, limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	body := map[string]any{"events": rows}
	if next != 0 {
		body["next"] = next
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) getRetention(w http.ResponseWriter, r *http.Request) {
	inbox := r.URL.Query().Get("inbox")
	days, set, err := s.Service.RetentionDays(s.requestCaller(r), inbox)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if !set && inbox == "" {
		days = s.RetentionDays
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "inbox": inbox, "override": set})
}

func (s *Server) putRetention(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Days  *int   `json:"days"`
		Inbox string `json:"inbox"`
	}
	if err := readJSON(r, &body); err != nil || body.Days == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "days is required")
		return
	}
	if err := s.Service.SetRetention(s.requestCaller(r), body.Inbox, *body.Days); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": *body.Days, "inbox": body.Inbox, "override": true})
}

func (s *Server) runRetention(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.Service.Sweep(r.Context(), s.requestCaller(r), s.RetentionDays)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": deleted})
}

func (s *Server) createInbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LocalPart  string `json:"localPart"`
		QuotaBytes int64  `json:"quotaBytes"`
	}
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	id, err := s.Service.CreateInbox(r.Context(), s.requestCaller(r), mailbackend.InboxSpec{
		DomainID:   s.DomainID,
		RoleID:     s.RoleID,
		Local:      body.LocalPart,
		QuotaBytes: body.QuotaBytes,
	}, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	address := strings.ToLower(strings.TrimSpace(body.LocalPart)) + "@" + s.Domain
	if err := s.Service.BindInbox(r.Context(), id, address); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id":      id,
		"address": address,
	})
}

func (s *Server) listInboxes(w http.ResponseWriter, r *http.Request) {
	page, err := pageFrom(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	inboxes, next, err := s.Service.ListInboxes(r.Context(), s.requestCaller(r), s.Domain, page)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if inboxes == nil {
		inboxes = []service.Inbox{}
	}
	body := map[string]any{"inboxes": inboxes}
	if token := encodeCursor(next); token != "" {
		body["next"] = token
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) deleteInbox(w http.ResponseWriter, r *http.Request) {
	if err := s.Service.DeleteInbox(r.Context(), s.requestCaller(r), r.PathValue("id")); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getAllowlist(w http.ResponseWriter, r *http.Request) {
	senders, set, err := s.Service.Allowlist(r.Context(), s.requestCaller(r), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"senders": senders, "set": set})
}

func (s *Server) putAllowlist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Senders *[]string `json:"senders"`
	}
	if err := readJSON(r, &body); err != nil || body.Senders == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "senders is required")
		return
	}
	if err := s.Service.SetAllowlist(r.Context(), s.requestCaller(r), r.PathValue("id"), *body.Senders); err != nil {
		writeServiceError(w, err)
		return
	}
	senders, set, err := s.Service.Allowlist(r.Context(), s.requestCaller(r), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"senders": senders, "set": set})
}

func (s *Server) getQuota(w http.ResponseWriter, r *http.Request) {
	q, err := s.Service.GetStorageQuota(r.Context(), s.requestCaller(r), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) putQuota(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LimitBytes *int64 `json:"limitBytes"`
	}
	if err := readJSON(r, &body); err != nil || body.LimitBytes == nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "limitBytes is required")
		return
	}
	if err := s.Service.SetStorageQuota(r.Context(), s.requestCaller(r), r.PathValue("id"), *body.LimitBytes); err != nil {
		writeServiceError(w, err)
		return
	}
	q, err := s.Service.GetStorageQuota(r.Context(), s.requestCaller(r), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) attachment(w http.ResponseWriter, r *http.Request) {
	data, meta, err := s.Service.Attachment(r.Context(), s.requestCaller(r), r.PathValue("id"), r.PathValue("messageId"), r.PathValue("blobId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	kind := meta.Type
	if kind == "" {
		kind = "application/octet-stream"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(meta.Name))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("id")
	caller := s.requestCaller(r)
	page, err := pageFrom(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	var messages []mailbackend.Message
	var next string
	if query := r.URL.Query().Get("q"); query != "" {
		messages, next, err = s.Service.Search(r.Context(), caller, accountID, query, page)
	} else {
		messages, next, err = s.Service.List(r.Context(), caller, accountID, page)
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if messages == nil {
		messages = []mailbackend.Message{}
	}
	body := map[string]any{"messages": messages}
	if token := encodeCursor(next); token != "" {
		body["next"] = token
	}
	writeJSON(w, http.StatusOK, body)
}

func pageFrom(r *http.Request) (mailbackend.Page, error) {
	page := mailbackend.Page{}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > mailbackend.MaxLimit {
			return page, fmt.Errorf("%w: limit must be from 1 to %d", service.ErrInvalid, mailbackend.MaxLimit)
		}
		page.Limit = n
	}
	token := r.URL.Query().Get("cursor")
	if token == "" {
		return page, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) == 0 {
		return page, fmt.Errorf("%w: cursor is not valid", service.ErrInvalid)
	}
	page.After = string(raw)
	return page, nil
}

func encodeCursor(id string) string {
	if id == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func (s *Server) generateInboxes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prefix string `json:"prefix"`
		Count  int    `json:"count"`
	}
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	result, err := s.Service.Generate(r.Context(), s.requestCaller(r), service.GenerateRequest{
		Prefix:   body.Prefix,
		Domain:   s.Domain,
		DomainID: s.DomainID,
		RoleID:   s.RoleID,
		Count:    body.Count,
		Wait:     body.Count <= s.Service.SyncLimit(),
	}, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if result.JobID != "" {
		writeJSON(w, http.StatusAccepted, map[string]string{"jobId": result.JobID, "status": "running"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"inboxes": result.Inboxes})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.Service.Job(r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	var body sendBody
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	err := s.Service.Deliver(r.Context(), s.requestCaller(r), body.outbound(r.PathValue("id")), r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "submitted"})
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	message, err := s.Service.Get(r.Context(), s.requestCaller(r), r.PathValue("id"), r.PathValue("messageId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, message)
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	err := s.Service.Delete(r.Context(), s.requestCaller(r), r.PathValue("id"), r.PathValue("messageId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) replyMessage(w http.ResponseWriter, r *http.Request) {
	var body sendBody
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	err := s.Service.Reply(r.Context(), s.requestCaller(r), r.PathValue("id"), r.PathValue("messageId"), body.outbound(""), r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "submitted"})
}

type sendBody struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Subject     string `json:"subject"`
	Text        string `json:"text"`
	Attachments []struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Data []byte `json:"data"`
	} `json:"attachments"`
}

func (b sendBody) outbound(accountID string) mailbackend.Outbound {
	files := make([]mailbackend.File, len(b.Attachments))
	for i, file := range b.Attachments {
		files[i] = mailbackend.File{Name: file.Name, Type: file.Type, Data: file.Data}
	}
	return mailbackend.Outbound{
		AccountID:   accountID,
		From:        b.From,
		To:          b.To,
		Subject:     b.Subject,
		Body:        b.Text,
		Attachments: files,
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	inbox := r.URL.Query().Get("inbox")
	if inbox == "" {
		writeProblem(w, http.StatusBadRequest, "Bad request", "inbox is required")
		return
	}
	var after int64
	if raw := r.URL.Query().Get("after"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "after must be an integer")
			return
		}
		after = value
	}
	timeout := 25 * time.Second
	if raw := r.URL.Query().Get("timeout"); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "timeout must be seconds")
			return
		}
		timeout = time.Duration(seconds) * time.Second
	}
	caller := s.requestCaller(r)
	if raw := r.URL.Query().Get("latest"); raw != "" {
		latest, err := strconv.ParseBool(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad request", "latest must be true or false")
			return
		}
		if latest && r.URL.Query().Has("after") {
			writeProblem(w, http.StatusBadRequest, "Bad request", "latest cannot be combined with after")
			return
		}
		if latest {
			head, err := s.Service.EventHead(r.Context(), caller, inbox)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			after = head
		}
	}
	records, err := s.Service.Events(r.Context(), caller, inbox, after, timeout)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if len(records) > 0 {
		after = records[len(records)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": records, "after": after})
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Inbox string `json:"inbox"`
		URL   string `json:"url"`
	}
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	id, secret, err := s.Service.SubscribeWebhook(r.Context(), s.requestCaller(r), body.Inbox, body.URL)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "secret": secret})
}

func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Inbai-Ingest")), []byte(s.IngestSecret)) != 1 || s.IngestSecret == "" {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "ingest secret was rejected")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	var body struct {
		Events []struct {
			Type string `json:"type"`
			Data struct {
				To      json.RawMessage `json:"to"`
				Details string          `json:"details"`
			} `json:"data"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	notices := make([]service.Notice, 0, len(body.Events))
	for _, event := range body.Events {
		notices = append(notices, service.Notice{Type: event.Type, To: recipientList(event.Data.To), Details: event.Data.Details})
	}
	synced, suppressed, err := s.Service.Observe(r.Context(), notices)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"synced": synced, "suppressed": suppressed})
}

func (s *Server) suppressions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Service.Suppressions(r.Context(), s.requestCaller(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"suppressions": list})
}

func recipientList(raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '"' {
		var one string
		if json.Unmarshal(raw, &one) == nil && strings.TrimSpace(one) != "" {
			return []string{one}
		}
		return nil
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.Service.Ready(r.Context()); err != nil {
		logError(w, err)
		writeProblem(w, http.StatusServiceUnavailable, "Not ready", "event database is not reachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = io.WriteString(w, s.Service.Metrics())
}

func readJSON(r *http.Request, dest any) error {
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dest)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// serviceProblems maps the service sentinels to a status and title. Their
// text is written for the caller and is shown as the problem detail.
var serviceProblems = []struct {
	err    error
	status int
	title  string
}{
	{service.ErrForbidden, http.StatusForbidden, "Forbidden"},
	{service.ErrQuota, http.StatusTooManyRequests, "Quota exceeded"},
	{service.ErrInvalid, http.StatusBadRequest, "Bad request"},
	{service.ErrNotFound, http.StatusNotFound, "Not found"},
	{service.ErrUnavailable, http.StatusServiceUnavailable, "Event store unavailable"},
	{service.ErrSuppressed, http.StatusConflict, "Suppressed"},
	{service.ErrUnsupported, http.StatusNotImplemented, "Not implemented"},
}

func writeServiceError(w http.ResponseWriter, err error) {
	for _, known := range serviceProblems {
		if errors.Is(err, known.err) {
			writeProblem(w, known.status, known.title, err.Error())
			return
		}
	}
	writeProblem(w, http.StatusBadGateway, "Mail backend failed", hiddenDetail(w, err))
}

// hiddenDetail is the problem detail for an error outside the service
// sentinels. The error text can name backend hosts, accounts, and database
// errors, so it goes to the request log line and the caller gets the request
// id. A refusal by the mail server is the exception: its reason is shown.
func hiddenDetail(w http.ResponseWriter, err error) string {
	id := logError(w, err)
	var refused *mailbackend.RefusedError
	if errors.As(err, &refused) {
		return "mail server refused the request: " + refused.Error()
	}
	if id == "" {
		return "internal error"
	}
	return "internal error, see the server log for request id " + id
}

// toolError is the error an MCP tool caller sees. It is the same text
// writeServiceError puts in the problem detail.
func toolError(w http.ResponseWriter, err error) error {
	for _, known := range serviceProblems {
		if errors.Is(err, known.err) {
			return err
		}
	}
	return errors.New(hiddenDetail(w, err))
}

func writeProblem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}
