// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// ServeHTTP handles one Streamable HTTP request.
// There is no protocol session. A POST carries one JSON-RPC message and the response is JSON.
func ServeHTTP(w http.ResponseWriter, r *http.Request, box Box) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	resp, ok := Dispatch(r.Context(), raw, box)
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp)
}

// Serve reads newline-delimited JSON-RPC from in and writes responses to out.
func Serve(ctx context.Context, in io.Reader, out io.Writer, box Box) error {
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		resp, ok := Dispatch(ctx, raw, box)
		if !ok {
			continue
		}
		if err := enc.Encode(json.RawMessage(resp)); err != nil {
			return err
		}
	}
}
