// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
	"unicode"
)

// logged writes one JSON line for each request. The line has no headers and no body.
func (s *Server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestID(r.Header.Get("X-Request-Id"))
		w.Header().Set("X-Request-Id", id)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		s.writeRequest(start, id, r.Method, r.URL.Path, rec.status)
	})
}

func (s *Server) writeRequest(start time.Time, id, method, path string, status int) {
	if s == nil || s.Log == nil {
		return
	}
	line, err := json.Marshal(struct {
		Time       string `json:"time"`
		RequestID  string `json:"requestId"`
		Method     string `json:"method"`
		Path       string `json:"path"`
		Status     int    `json:"status"`
		DurationMs int64  `json:"durationMs"`
	}{
		Time:       start.UTC().Format(time.RFC3339Nano),
		RequestID:  id,
		Method:     method,
		Path:       path,
		Status:     status,
		DurationMs: time.Since(start).Milliseconds(),
	})
	if err != nil {
		return
	}
	_, _ = s.Log.Write(append(line, '\n'))
}

func requestID(got string) string {
	if validRequestID(got) {
		return got
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf)
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wrote {
		return
	}
	s.wrote = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(body []byte) (int, error) {
	if !s.wrote {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(body)
}
