// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/mcp"
)

func mcpCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 || args[0] != "serve" {
		writef(stderr, "usage: inbai mcp serve [--config path]\n")
		return 2
	}
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	path, err := configFilePath(*configPath)
	if err != nil {
		writef(stderr, "config: %s\n", err)
		return 1
	}
	cfg, err := readConfig(path)
	if err != nil {
		writef(stderr, "read config: %s\n", err)
		return 1
	}
	base := cfg.APIURL
	if base == "" {
		base = config.DefaultAPIURL
	}
	token, src, err := mcpCredential(context.Background(), base, cfg, getenv)
	if err != nil {
		writef(stderr, "mcp: %s\n", err)
		if errors.Is(err, errMissingCredential) {
			return 2
		}
		return 1
	}
	err = mcp.Serve(context.Background(), stdin, stdout, apiBox{client: apiClient{base: base, token: token, src: src}})
	if err != nil {
		writef(stderr, "mcp: %s\n", err)
		return 1
	}
	return 0
}

var errMissingCredential = errors.New("missing credential")

// mcpCredential resolves the bearer token (env access token, env client
// credentials, env operator token, config client credentials, config
// operator token). When it comes from a client ID and secret it also returns
// a tokenSource that renews it; a static token returns nil.
func mcpCredential(ctx context.Context, base string, cfg config.File, getenv func(string) string) (string, *tokenSource, error) {
	exchange := func(id, secret string) (string, *tokenSource, error) {
		src := &tokenSource{base: base, id: id, secret: secret, now: time.Now}
		token, err := src.current(ctx)
		if err != nil {
			return "", nil, err
		}
		return token, src, nil
	}
	if getenv != nil {
		if token := getenv("INBAI_ACCESS_TOKEN"); token != "" {
			return token, nil, nil
		}
		id := getenv("INBAI_CLIENT_ID")
		secret := getenv("INBAI_CLIENT_SECRET")
		if id != "" || secret != "" {
			if id == "" || secret == "" {
				return "", nil, errMissingCredential
			}
			return exchange(id, secret)
		}
		if token := getenv("INBAI_OPERATOR_TOKEN"); token != "" {
			return token, nil, nil
		}
	}
	if cfg.ClientID != "" && cfg.ClientSecret != "" {
		return exchange(cfg.ClientID, cfg.ClientSecret)
	}
	if cfg.OperatorToken != "" {
		return cfg.OperatorToken, nil, nil
	}
	return "", nil, errMissingCredential
}

// tokenRefreshMargin is how long before expiry a token is renewed.
const tokenRefreshMargin = 30 * time.Second

// tokenSource renews an access token obtained with client credentials. It is
// shared by every copy of an apiClient and is safe for concurrent use.
type tokenSource struct {
	base, id, secret string
	now              func() time.Time

	mu      sync.Mutex
	token   string
	renewAt time.Time // zero when the server gave no expiry; only a 401 renews then
}

// current returns a token, exchanging a new one when none is held or the held
// one is within the margin of expiring.
func (s *tokenSource) current(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && (s.renewAt.IsZero() || s.now().Before(s.renewAt)) {
		return s.token, nil
	}
	return s.renewLocked(ctx)
}

// rejected is called when the server answered 401 to token. It exchanges a new
// token unless another caller already replaced it.
func (s *tokenSource) rejected(ctx context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && s.token != token {
		return s.token, nil
	}
	return s.renewLocked(ctx)
}

func (s *tokenSource) renewLocked(ctx context.Context) (string, error) {
	token, ttl, err := exchangeTokenTTL(ctx, s.base, s.id, s.secret)
	if err != nil {
		return "", err
	}
	s.token = token
	s.renewAt = time.Time{}
	if ttl > 0 {
		margin := tokenRefreshMargin
		if ttl <= 2*margin {
			margin = ttl / 2
		}
		s.renewAt = s.now().Add(ttl - margin)
	}
	return token, nil
}

func exchangeToken(ctx context.Context, base, id, secret string) (string, error) {
	token, _, err := exchangeTokenTTL(ctx, base, id, secret)
	return token, err
}

func exchangeTokenTTL(ctx context.Context, base, id, secret string) (string, time.Duration, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", id)
	form.Set("client_secret", secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Detail      string `json:"error_description"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", 0, err
	}
	if res.StatusCode != http.StatusOK || body.AccessToken == "" {
		if body.Error != "" {
			return "", 0, fmt.Errorf("token exchange: %s", body.Error)
		}
		return "", 0, fmt.Errorf("token exchange: %s", res.Status)
	}
	return body.AccessToken, time.Duration(body.ExpiresIn) * time.Second, nil
}

type apiBox struct {
	client apiClient
}

func (b apiBox) List(ctx context.Context, inbox, query string) ([]mailbackend.Message, error) {
	messages, _, err := b.client.listMessages(ctx, inbox, query, "", 0)
	return toMessages(messages), err
}

func (b apiBox) Get(ctx context.Context, inbox, messageID string) (mailbackend.Message, error) {
	message, err := b.client.getMessage(ctx, inbox, messageID)
	converted := toMessages([]apiMessage{message})
	return converted[0], err
}

func (b apiBox) Send(ctx context.Context, inbox string, msg mailbackend.Outbound) error {
	return b.client.send(ctx, inbox, apiSend{From: msg.From, To: msg.To, Subject: msg.Subject, Text: msg.Body})
}

func (b apiBox) Reply(ctx context.Context, inbox, messageID string, msg mailbackend.Outbound) error {
	return b.client.reply(ctx, inbox, messageID, apiSend{From: msg.From, To: msg.To, Subject: msg.Subject, Text: msg.Body})
}

func (b apiBox) Delete(ctx context.Context, inbox, messageID string) error {
	return b.client.deleteMessage(ctx, inbox, messageID)
}

func (b apiBox) Wait(ctx context.Context, inbox string, after *int64, timeout time.Duration) ([]mailbackend.Message, int64, error) {
	seconds := int(timeout.Seconds())
	if seconds < 0 {
		seconds = 0
	}
	records, next, err := b.client.waitEvents(ctx, inbox, after, seconds)
	if err != nil {
		return nil, 0, err
	}
	out := make([]mailbackend.Message, len(records))
	for i, record := range records {
		out[i] = mailbackend.Message{ID: record.MessageID, Subject: record.Kind}
	}
	return out, next, nil
}

// waitEvents long-polls the event feed. A nil after asks the server to start at the newest event,
// so the head is read and waited on in one request.
func (c apiClient) waitEvents(ctx context.Context, inbox string, after *int64, timeout int) ([]apiEvent, int64, error) {
	query := url.Values{}
	query.Set("inbox", inbox)
	if after != nil {
		query.Set("after", strconv.FormatInt(*after, 10))
	} else {
		query.Set("latest", "true")
	}
	query.Set("timeout", strconv.Itoa(timeout))
	var out struct {
		Events []apiEvent `json:"events"`
		After  int64      `json:"after"`
	}
	_, err := c.call(ctx, http.MethodGet, "/v1/events?"+query.Encode(), nil, &out)
	return out.Events, out.After, err
}

func toMessages(in []apiMessage) []mailbackend.Message {
	out := make([]mailbackend.Message, len(in))
	for i, message := range in {
		out[i] = mailbackend.Message{ID: message.ID, Subject: message.Subject, Preview: message.Preview, Text: message.Text}
		for _, file := range message.Attachments {
			out[i].Attachments = append(out[i].Attachments, mailbackend.Attachment{Name: file.Name, Type: file.Type, Size: file.Size})
		}
	}
	return out
}
