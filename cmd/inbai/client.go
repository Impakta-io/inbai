// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// apiClient is the operator CLI's handwritten client for the inbaid HTTP API.
type apiClient struct {
	base  string
	token string
	http  *http.Client
	poll  time.Duration
}

type apiInbox struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

type apiMessage struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	Preview     string `json:"preview"`
	Text        string `json:"text,omitempty"`
	Attachments []struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Size   int    `json:"size"`
		BlobID string `json:"blobId,omitempty"`
	} `json:"attachments,omitempty"`
}

type apiSend struct {
	From        string    `json:"from"`
	To          string    `json:"to"`
	Subject     string    `json:"subject,omitempty"`
	Text        string    `json:"text,omitempty"`
	Attachments []apiFile `json:"attachments,omitempty"`
}

type apiFile struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Data []byte `json:"data"`
}

func (c apiClient) client() *http.Client {
	if c.http != nil {
		return c.http
	}
	return http.DefaultClient
}

func (c apiClient) pause() time.Duration {
	if c.poll > 0 {
		return c.poll
	}
	return 200 * time.Millisecond
}

func (c apiClient) createInbox(ctx context.Context, local string) (apiInbox, error) {
	var out apiInbox
	_, err := c.call(ctx, http.MethodPost, "/v1/inboxes", map[string]string{"localPart": local}, &out)
	return out, err
}

func (c apiClient) generate(ctx context.Context, prefix string, count int, progress func(done, total int)) ([]apiInbox, error) {
	status, raw, err := c.callRaw(ctx, http.MethodPost, "/v1/inboxes/generate", map[string]any{"prefix": prefix, "count": count})
	if err != nil {
		return nil, err
	}
	if status == http.StatusCreated {
		var out struct {
			Inboxes []apiInbox `json:"inboxes"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		if progress != nil {
			progress(len(out.Inboxes), len(out.Inboxes))
		}
		return out.Inboxes, nil
	}
	var accepted struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(raw, &accepted); err != nil {
		return nil, err
	}
	if accepted.JobID == "" {
		return nil, fmt.Errorf("generate returned no job id")
	}
	for {
		var job struct {
			Status  string     `json:"status"`
			Total   int        `json:"total"`
			Created int        `json:"created"`
			Inboxes []apiInbox `json:"inboxes"`
			Error   string     `json:"error"`
		}
		if _, err := c.call(ctx, http.MethodGet, "/v1/jobs/"+accepted.JobID, nil, &job); err != nil {
			return nil, err
		}
		if progress != nil {
			progress(job.Created, job.Total)
		}
		switch job.Status {
		case "done":
			return job.Inboxes, nil
		case "failed":
			if job.Error == "" {
				job.Error = "generate failed"
			}
			return nil, fmt.Errorf("%s", job.Error)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.pause()):
		}
	}
}

func (c apiClient) listMessages(ctx context.Context, accountID, query string) ([]apiMessage, error) {
	path := "/v1/inboxes/" + accountID + "/messages"
	if query != "" {
		path += "?q=" + query
	}
	var out struct {
		Messages []apiMessage `json:"messages"`
	}
	_, err := c.call(ctx, http.MethodGet, path, nil, &out)
	return out.Messages, err
}

func (c apiClient) getMessage(ctx context.Context, accountID, messageID string) (apiMessage, error) {
	var out apiMessage
	_, err := c.call(ctx, http.MethodGet, "/v1/inboxes/"+accountID+"/messages/"+messageID, nil, &out)
	return out, err
}

func (c apiClient) send(ctx context.Context, accountID string, msg apiSend) error {
	_, err := c.call(ctx, http.MethodPost, "/v1/inboxes/"+accountID+"/messages", msg, nil)
	return err
}

func (c apiClient) reply(ctx context.Context, accountID, messageID string, msg apiSend) error {
	_, err := c.call(ctx, http.MethodPost, "/v1/inboxes/"+accountID+"/messages/"+messageID+"/reply", msg, nil)
	return err
}

func (c apiClient) listInboxes(ctx context.Context) ([]apiInbox, error) {
	var out struct {
		Inboxes []apiInbox `json:"inboxes"`
	}
	_, err := c.call(ctx, http.MethodGet, "/v1/inboxes", nil, &out)
	return out.Inboxes, err
}

func (c apiClient) deleteInbox(ctx context.Context, id string) error {
	_, err := c.call(ctx, http.MethodDelete, "/v1/inboxes/"+id, nil, nil)
	return err
}

func (c apiClient) download(ctx context.Context, accountID, messageID, blobID string) ([]byte, error) {
	_, raw, err := c.callRaw(ctx, http.MethodGet, "/v1/inboxes/"+accountID+"/messages/"+messageID+"/attachments/"+blobID, nil)
	return raw, err
}

func (c apiClient) deleteMessage(ctx context.Context, accountID, messageID string) error {
	_, err := c.call(ctx, http.MethodDelete, "/v1/inboxes/"+accountID+"/messages/"+messageID, nil, nil)
	return err
}

type apiEvent struct {
	ID        int64  `json:"id"`
	MessageID string `json:"messageId"`
	Kind      string `json:"kind"`
}

func (c apiClient) events(ctx context.Context, accountID string, after int64, timeout int) ([]apiEvent, error) {
	path := fmt.Sprintf("/v1/events?inbox=%s&after=%d&timeout=%d", url.QueryEscape(accountID), after, timeout)
	var out struct {
		Events []apiEvent `json:"events"`
	}
	_, err := c.call(ctx, http.MethodGet, path, nil, &out)
	return out.Events, err
}

func (c apiClient) call(ctx context.Context, method, path string, in, out any) (int, error) {
	status, raw, err := c.callRaw(ctx, method, path, in)
	if err != nil {
		return status, err
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return status, err
		}
	}
	return status, nil
}

func (c apiClient) callRaw(ctx context.Context, method, path string, in any) (int, []byte, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.base, "/")+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode >= 300 {
		var problem struct {
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(raw, &problem)
		if problem.Detail == "" {
			problem.Detail = resp.Status
		}
		return resp.StatusCode, raw, fmt.Errorf("%s", problem.Detail)
	}
	return resp.StatusCode, raw, nil
}
