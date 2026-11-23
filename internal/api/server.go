// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/service"
)

// Server is the REST transport. It authenticates the operator token and calls the service layer.
type Server struct {
	Service  *service.Service
	Token    string
	Domain   string
	DomainID string
	RoleID   string
}

// Handler returns the HTTP routes under /v1.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/inboxes", s.createInbox)
	mux.HandleFunc("GET /v1/inboxes/{id}/messages", s.listMessages)
	mux.HandleFunc("POST /v1/inboxes/{id}/messages", s.sendMessage)
	mux.HandleFunc("GET /v1/inboxes/{id}/messages/{messageId}", s.getMessage)
	mux.HandleFunc("DELETE /v1/inboxes/{id}/messages/{messageId}", s.deleteMessage)
	mux.HandleFunc("POST /v1/inboxes/{id}/messages/{messageId}/reply", s.replyMessage)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "bearer token is missing or rejected")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	if s.Token == "" {
		return false
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func (s *Server) createInbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LocalPart string `json:"localPart"`
	}
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	id, err := s.Service.CreateInbox(r.Context(), service.Caller{Operator: true}, mailbackend.InboxSpec{
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
	caller := service.Caller{Operator: true}
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

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	err := s.Service.Deliver(r.Context(), service.Caller{Operator: true}, mailbackend.Outbound{
		AccountID: r.PathValue("id"),
		From:      body.From,
		To:        body.To,
		Subject:   body.Subject,
		Body:      body.Text,
	}, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "submitted"})
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	message, err := s.Service.Get(r.Context(), service.Caller{Operator: true}, r.PathValue("id"), r.PathValue("messageId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, message)
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	err := s.Service.Delete(r.Context(), service.Caller{Operator: true}, r.PathValue("id"), r.PathValue("messageId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) replyMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}
	if err := readJSON(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad request", "request body is not JSON")
		return
	}
	err := s.Service.Reply(r.Context(), service.Caller{Operator: true}, r.PathValue("id"), r.PathValue("messageId"), mailbackend.Outbound{
		From:    body.From,
		To:      body.To,
		Subject: body.Subject,
		Body:    body.Text,
	}, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "submitted"})
}

func readJSON(r *http.Request, dest any) error {
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
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
