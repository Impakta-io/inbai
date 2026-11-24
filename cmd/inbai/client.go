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
	"strconv"
	"strings"
	"time"
)

// apiClient is the operator CLI's handwritten client for the inbaid HTTP API.
type apiClient struct {
	base  string
	token string
	// src renews token when it came from a client ID and secret. It is nil for a static token.
	src  *tokenSource
	http *http.Client
	poll time.Duration
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

func (c apiClient) createAgent(ctx context.Context, name string) (string, string, error) {
	var out struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	_, err := c.call(ctx, http.MethodPost, "/v1/agents", map[string]string{"name": name}, &out)
	return out.ID, out.Secret, err
}

func (c apiClient) grant(ctx context.Context, agent, inbox, permission string) error {
	_, err := c.call(ctx, http.MethodPost, "/v1/grants", map[string]string{
		"agent": agent, "inbox": inbox, "permission": permission,
	}, nil)
	return err
}

func (c apiClient) suspendAgent(ctx context.Context, name string) error {
	_, err := c.call(ctx, http.MethodPost, "/v1/agents/"+pathSegment(name)+"/suspend", map[string]string{}, nil)
	return err
}

func (c apiClient) revoke(ctx context.Context, agent, inbox, permission string) error {
	path := "/v1/grants/" + pathSegment(agent) + "/" + pathSegment(inbox) + "/" + pathSegment(permission)
	_, err := c.call(ctx, http.MethodDelete, path, nil, nil)
	return err
}

func (c apiClient) audit(ctx context.Context, after int64, limit int) ([]apiAudit, int64, error) {
	query := url.Values{}
	if after > 0 {
		query.Set("after", strconv.FormatInt(after, 10))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/audit"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out struct {
		Events []apiAudit `json:"events"`
		Next   int64      `json:"next"`
	}
	_, err := c.call(ctx, http.MethodGet, path, nil, &out)
	return out.Events, out.Next, err
}

type apiRetention struct {
	Days     int    `json:"days"`
	Inbox    string `json:"inbox"`
	Override bool   `json:"override"`
}

func (c apiClient) retention(ctx context.Context, inbox string) (apiRetention, error) {
	path := "/v1/retention"
	if inbox != "" {
		path += "?inbox=" + url.QueryEscape(inbox)
	}
	var out apiRetention
	_, err := c.call(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c apiClient) setRetention(ctx context.Context, inbox string, days int) (apiRetention, error) {
	var out apiRetention
	_, err := c.call(ctx, http.MethodPut, "/v1/retention", map[string]any{"inbox": inbox, "days": days}, &out)
	return out, err
}

func (c apiClient) runRetention(ctx context.Context) (int, error) {
	var out struct {
		Deleted int `json:"deleted"`
	}
	_, err := c.call(ctx, http.MethodPost, "/v1/retention/run", map[string]any{}, &out)
	return out.Deleted, err
}

func (c apiClient) createInbox(ctx context.Context, local string, quotaBytes int64) (apiInbox, error) {
	body := map[string]any{"localPart": local}
	if quotaBytes > 0 {
		body["quotaBytes"] = quotaBytes
	}
	var out apiInbox
	_, err := c.call(ctx, http.MethodPost, "/v1/inboxes", body, &out)
	return out, err
}

type apiStorageQuota struct {
	LimitBytes int64 `json:"limitBytes"`
	UsedBytes  int64 `json:"usedBytes"`
}

func (c apiClient) storageQuota(ctx context.Context, inbox string) (apiStorageQuota, error) {
	var out apiStorageQuota
	_, err := c.call(ctx, http.MethodGet, "/v1/inboxes/"+pathSegment(inbox)+"/quota", nil, &out)
	return out, err
}

func (c apiClient) setStorageQuota(ctx context.Context, inbox string, limitBytes int64) (apiStorageQuota, error) {
	var out apiStorageQuota
	_, err := c.call(ctx, http.MethodPut, "/v1/inboxes/"+pathSegment(inbox)+"/quota", map[string]int64{"limitBytes": limitBytes}, &out)
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
		if _, err := c.call(ctx, http.MethodGet, "/v1/jobs/"+pathSegment(accepted.JobID), nil, &job); err != nil {
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

func (c apiClient) listMessages(ctx context.Context, accountID, query, cursor string, limit int) ([]apiMessage, string, error) {
	path := withPage("/v1/inboxes/"+pathSegment(accountID)+"/messages", map[string]string{"q": query, "cursor": cursor}, limit)
	var out struct {
		Messages []apiMessage `json:"messages"`
		Next     string       `json:"next"`
	}
	_, err := c.call(ctx, http.MethodGet, path, nil, &out)
	return out.Messages, out.Next, err
}

func (c apiClient) getMessage(ctx context.Context, accountID, messageID string) (apiMessage, error) {
	var out apiMessage
	_, err := c.call(ctx, http.MethodGet, "/v1/inboxes/"+pathSegment(accountID)+"/messages/"+pathSegment(messageID), nil, &out)
	return out, err
}

func (c apiClient) send(ctx context.Context, accountID string, msg apiSend) error {
	_, err := c.call(ctx, http.MethodPost, "/v1/inboxes/"+pathSegment(accountID)+"/messages", msg, nil)
	return err
}

func (c apiClient) reply(ctx context.Context, accountID, messageID string, msg apiSend) error {
	_, err := c.call(ctx, http.MethodPost, "/v1/inboxes/"+pathSegment(accountID)+"/messages/"+pathSegment(messageID)+"/reply", msg, nil)
	return err
}

func (c apiClient) listInboxes(ctx context.Context, cursor string, limit int) ([]apiInbox, string, error) {
	var out struct {
		Inboxes []apiInbox `json:"inboxes"`
		Next    string     `json:"next"`
	}
	_, err := c.call(ctx, http.MethodGet, withPage("/v1/inboxes", map[string]string{"cursor": cursor}, limit), nil, &out)
	return out.Inboxes, out.Next, err
}

// pathSegment escapes one caller-supplied value for use as a single URL path
// segment. url.PathEscape leaves "." and ".." alone, which a client or router
// would collapse, so those two are escaped here.
func pathSegment(s string) string {
	switch s {
	case ".":
		return "%2E"
	case "..":
		return "%2E%2E"
	}
	return url.PathEscape(s)
}

func withPage(path string, extra map[string]string, limit int) string {
	values := url.Values{}
	for key, value := range extra {
		if value != "" {
			values.Set(key, value)
		}
	}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

func (c apiClient) deleteInbox(ctx context.Context, id string) error {
	_, err := c.call(ctx, http.MethodDelete, "/v1/inboxes/"+pathSegment(id), nil, nil)
	return err
}

func (c apiClient) download(ctx context.Context, accountID, messageID, blobID string) ([]byte, error) {
	_, raw, err := c.callRaw(ctx, http.MethodGet, "/v1/inboxes/"+pathSegment(accountID)+"/messages/"+pathSegment(messageID)+"/attachments/"+pathSegment(blobID), nil)
	return raw, err
}

func (c apiClient) deleteMessage(ctx context.Context, accountID, messageID string) error {
	_, err := c.call(ctx, http.MethodDelete, "/v1/inboxes/"+pathSegment(accountID)+"/messages/"+pathSegment(messageID), nil, nil)
	return err
}

type apiEvent struct {
	ID        int64  `json:"id"`
	MessageID string `json:"messageId"`
	Kind      string `json:"kind"`
}

type apiAudit struct {
	ID     int64  `json:"id"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
}

type apiAllowlist struct {
	Senders []string `json:"senders"`
	Set     bool     `json:"set"`
}

func (c apiClient) allowlist(ctx context.Context, inbox string) (apiAllowlist, error) {
	var out apiAllowlist
	_, err := c.call(ctx, http.MethodGet, "/v1/inboxes/"+pathSegment(inbox)+"/allowlist", nil, &out)
	return out, err
}

func (c apiClient) setAllowlist(ctx context.Context, inbox string, senders []string) (apiAllowlist, error) {
	if senders == nil {
		senders = []string{}
	}
	var out apiAllowlist
	_, err := c.call(ctx, http.MethodPut, "/v1/inboxes/"+pathSegment(inbox)+"/allowlist", map[string]any{"senders": senders}, &out)
	return out, err
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
	token := c.token
	if c.src != nil {
		var err error
		if token, err = c.src.current(ctx); err != nil {
			return 0, nil, err
		}
	}
	status, raw, err := c.doRequest(ctx, method, path, in, token)
	if status == http.StatusUnauthorized && c.src != nil {
		fresh, rerr := c.src.rejected(ctx, token)
		if rerr != nil {
			return 0, nil, rerr
		}
		return c.doRequest(ctx, method, path, in, fresh)
	}
	return status, raw, err
}

// maxResponseBytes is above the 25 MB the server allows for an attachment, so a
// legitimate download is never cut short. A larger response is an error.
const maxResponseBytes = 32 << 20

func (c apiClient) doRequest(ctx context.Context, method, path string, in any, token string) (int, []byte, error) {
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
	req.Header.Set("Authorization", "Bearer "+token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(raw) > maxResponseBytes {
		return resp.StatusCode, nil, fmt.Errorf("response is larger than %d MiB", maxResponseBytes>>20)
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
