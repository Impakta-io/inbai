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
	"strings"
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
	token, err := mcpToken(context.Background(), base, getenv)
	if err != nil {
		writef(stderr, "mcp: %s\n", err)
		if errors.Is(err, errMissingCredential) {
			return 2
		}
		return 1
	}
	err = mcp.Serve(context.Background(), stdin, stdout, apiBox{client: apiClient{base: base, token: token}})
	if err != nil {
		writef(stderr, "mcp: %s\n", err)
		return 1
	}
	return 0
}

var errMissingCredential = errors.New("missing credential")

func mcpToken(ctx context.Context, base string, getenv func(string) string) (string, error) {
	if getenv == nil {
		return "", errMissingCredential
	}
	if token := getenv("INBAI_ACCESS_TOKEN"); token != "" {
		return token, nil
	}
	id := getenv("INBAI_CLIENT_ID")
	secret := getenv("INBAI_CLIENT_SECRET")
	if id != "" || secret != "" {
		if id == "" || secret == "" {
			return "", errMissingCredential
		}
		return exchangeToken(ctx, base, id, secret)
	}
	if token := getenv("INBAI_OPERATOR_TOKEN"); token != "" {
		return token, nil
	}
	return "", errMissingCredential
}

func exchangeToken(ctx context.Context, base, id, secret string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", id)
	form.Set("client_secret", secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		AccessToken string `json:"access_token"`
		Detail      string `json:"error_description"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK || body.AccessToken == "" {
		if body.Error != "" {
			return "", fmt.Errorf("token exchange: %s", body.Error)
		}
		return "", fmt.Errorf("token exchange: %s", res.Status)
	}
	return body.AccessToken, nil
}

type apiBox struct {
	client apiClient
}

func (b apiBox) List(ctx context.Context, inbox, query string) ([]mailbackend.Message, error) {
	if query != "" {
		query = url.QueryEscape(query)
	}
	messages, err := b.client.listMessages(ctx, inbox, query)
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

func (b apiBox) Wait(ctx context.Context, inbox string, timeout time.Duration) ([]mailbackend.Message, error) {
	seconds := int(timeout.Seconds())
	if seconds < 0 {
		seconds = 0
	}
	records, err := b.client.events(ctx, inbox, 0, seconds)
	if err != nil {
		return nil, err
	}
	out := make([]mailbackend.Message, len(records))
	for i, record := range records {
		out[i] = mailbackend.Message{ID: record.MessageID, Subject: record.Kind}
	}
	return out, nil
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
