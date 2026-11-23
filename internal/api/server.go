// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Impakta-io/inbai/internal/auth"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/mcp"
	"github.com/Impakta-io/inbai/internal/service"
)

// Server is the REST transport. It authenticates the caller and calls the service layer.
type Server struct {
	Service   *service.Service
	Directory *auth.Directory
	Token     string
	Issuer    string
	Domain    string
	DomainID  string
	RoleID    string
}

type callerKey struct{}

// Handler returns the HTTP routes under /v1.
func (s *Server) Handler() http.Handler {
	public := http.NewServeMux()
	public.HandleFunc("POST /oauth/token", s.oauthToken)
	public.HandleFunc("GET /.well-known/oauth-authorization-server", s.authorizationMetadata)
	public.HandleFunc("GET /.well-known/oauth-protected-resource", s.resourceMetadata)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", s.mcp)
	mux.HandleFunc("GET /mcp", s.mcp)
	mux.HandleFunc("POST /v1/agents", s.createAgent)
	mux.HandleFunc("POST /v1/agents/{name}/suspend", s.suspendAgent)
	mux.HandleFunc("POST /v1/grants", s.createGrant)
	mux.HandleFunc("POST /v1/inboxes", s.createInbox)
	mux.HandleFunc("POST /v1/inboxes/generate", s.generateInboxes)
	mux.HandleFunc("GET /v1/jobs/{id}", s.getJob)
	mux.HandleFunc("GET /v1/inboxes/{id}/messages", s.listMessages)
	mux.HandleFunc("POST /v1/inboxes/{id}/messages", s.sendMessage)
	mux.HandleFunc("GET /v1/inboxes/{id}/messages/{messageId}", s.getMessage)
	mux.HandleFunc("DELETE /v1/inboxes/{id}/messages/{messageId}", s.deleteMessage)
	mux.HandleFunc("POST /v1/inboxes/{id}/messages/{messageId}/reply", s.replyMessage)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" || strings.HasPrefix(r.URL.Path, "/.well-known/") {
			public.ServeHTTP(w, r)
			return
		}
		caller, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	})
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	mcp.ServeHTTP(w, r, mcp.ServiceBox{Service: s.Service, Caller: s.requestCaller(r)})
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

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if s.Directory == nil {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
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
	if err != nil {
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

func (s *Server) createInbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LocalPart string `json:"localPart"`
	}
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	id, err := s.Service.CreateInbox(r.Context(), s.requestCaller(r), mailbackend.InboxSpec{
		DomainID: s.DomainID,
		RoleID:   s.RoleID,
		Local:    body.LocalPart,
	}, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id":      id,
		"address": strings.ToLower(strings.TrimSpace(body.LocalPart)) + "@" + s.Domain,
	})
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("id")
	caller := s.requestCaller(r)
	var messages []mailbackend.Message
	var err error
	if query := r.URL.Query().Get("q"); query != "" {
		messages, err = s.Service.Search(r.Context(), caller, accountID, query)
	} else {
		messages, err = s.Service.List(r.Context(), caller, accountID)
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if messages == nil {
		messages = []mailbackend.Message{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
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

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		writeProblem(w, http.StatusForbidden, "Forbidden", err.Error())
	case errors.Is(err, service.ErrQuota):
		writeProblem(w, http.StatusTooManyRequests, "Quota exceeded", err.Error())
	case errors.Is(err, service.ErrInvalid):
		writeProblem(w, http.StatusBadRequest, "Bad request", err.Error())
	case errors.Is(err, service.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", err.Error())
	default:
		writeProblem(w, http.StatusBadGateway, "Mail backend failed", err.Error())
	}
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
