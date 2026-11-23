// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Impakta-io/inbai/internal/config"
)

func inboxCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		writef(stderr, "usage: inbai inbox [create|generate]\n")
		return 2
	}
	switch args[0] {
	case "generate":
		return inboxGenerate(args[1:], stdout, stderr, getenv)
	case "create":
	default:
		writef(stderr, "usage: inbai inbox [create|generate]\n")
		return 2
	}
	fs := flag.NewFlagSet("inbox create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		writef(stderr, "usage: inbai inbox create <local-part>\n")
		return 2
	}
	local := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	cfg, path, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	created, err := client.createInbox(ctx, local)
	if err != nil {
		writef(stderr, "inbox create: %s\n", err)
		return 1
	}
	if cfg.Inboxes == nil {
		cfg.Inboxes = map[string]string{}
	}
	cfg.Inboxes[created.Address] = created.ID
	if err := writeConfig(path, cfg); err != nil {
		writef(stderr, "write config: %s\n", err)
		return 1
	}
	emit(stdout, *asJSON, "Created "+created.Address+"\n", created)
	return 0
}

func messageCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		writef(stderr, "usage: inbai message [send|list|get|reply|delete]\n")
		return 2
	}
	switch args[0] {
	case "list":
		return messageList(args[1:], stdout, stderr, getenv)
	case "send":
		return messageSend(args[1:], stdout, stderr, getenv)
	case "get":
		return messageGet(args[1:], stdout, stderr, getenv)
	case "reply":
		return messageReply(args[1:], stdout, stderr, getenv)
	case "delete":
		return messageDelete(args[1:], stdout, stderr, getenv)
	default:
		writef(stderr, "unknown message command %q\n", args[0])
		return 2
	}
}

func messageList(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("message list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	asJSON := fs.Bool("json", false, "print JSON")
	query := fs.String("query", "", "search message text")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		writef(stderr, "usage: inbai message list [--query text] <address>\n")
		return 2
	}
	cfg, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	accountID, ok := cfg.Inboxes[fs.Arg(0)]
	if !ok {
		writef(stderr, "message list: %s is not in the config. Create it with inbai inbox create.\n", fs.Arg(0))
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	messages, err := client.listMessages(ctx, accountID, url.QueryEscape(*query))
	if err != nil {
		writef(stderr, "message list: %s\n", err)
		return 1
	}
	if *asJSON {
		emit(stdout, true, "", map[string]any{"messages": messages})
		return 0
	}
	if len(messages) == 0 {
		writef(stdout, "No messages.\n")
		return 0
	}
	for _, message := range messages {
		writef(stdout, "%s\t%s\n", message.Subject, message.Preview)
	}
	return 0
}

func messageSend(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("message send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	asJSON := fs.Bool("json", false, "print JSON")
	from := fs.String("from", "", "sender address")
	to := fs.String("to", "", "recipient address")
	subject := fs.String("subject", "", "subject")
	body := fs.String("body", "", "plain text body")
	attach := fs.String("attach", "", "file to attach")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" || *to == "" || *subject == "" {
		writef(stderr, "usage: inbai message send --from <address> --to <address> --subject <text> [--body <text>] [--attach <file>]\n")
		return 2
	}
	cfg, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	accountID, ok := cfg.Inboxes[*from]
	if !ok {
		writef(stderr, "message send: %s is not in the config. Create it with inbai inbox create.\n", *from)
		return 1
	}
	files, code := readAttach(*attach, stderr)
	if code != 0 {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := client.send(ctx, accountID, apiSend{From: *from, To: *to, Subject: *subject, Text: *body, Attachments: files})
	if err != nil {
		writef(stderr, "message send: %s\n", err)
		return 1
	}
	emit(stdout, *asJSON, "Sent "+*subject+" to "+*to+"\n", map[string]string{"status": "submitted"})
	return 0
}

func messageGet(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("message get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		writef(stderr, "usage: inbai message get <address> <message-id>\n")
		return 2
	}
	cfg, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	accountID, ok := cfg.Inboxes[fs.Arg(0)]
	if !ok {
		writef(stderr, "message get: %s is not in the config. Create it with inbai inbox create.\n", fs.Arg(0))
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	message, err := client.getMessage(ctx, accountID, fs.Arg(1))
	if err != nil {
		writef(stderr, "message get: %s\n", err)
		return 1
	}
	if *asJSON {
		emit(stdout, true, "", message)
		return 0
	}
	writef(stdout, "%s\n%s\n", message.Subject, message.Text)
	for _, file := range message.Attachments {
		writef(stdout, "attachment %s\n", file.Name)
	}
	return 0
}

func messageReply(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("message reply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	asJSON := fs.Bool("json", false, "print JSON")
	to := fs.String("to", "", "recipient address")
	body := fs.String("body", "", "plain text body")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 || *to == "" {
		writef(stderr, "usage: inbai message reply <address> <message-id> --to <address> [--body <text>]\n")
		return 2
	}
	cfg, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	accountID, ok := cfg.Inboxes[fs.Arg(0)]
	if !ok {
		writef(stderr, "message reply: %s is not in the config. Create it with inbai inbox create.\n", fs.Arg(0))
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := client.reply(ctx, accountID, fs.Arg(1), apiSend{From: fs.Arg(0), To: *to, Text: *body})
	if err != nil {
		writef(stderr, "message reply: %s\n", err)
		return 1
	}
	emit(stdout, *asJSON, "Replied to "+fs.Arg(1)+"\n", map[string]string{"status": "submitted"})
	return 0
}

func messageDelete(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("message delete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/inbai/config.json)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		writef(stderr, "usage: inbai message delete <address> <message-id>\n")
		return 2
	}
	cfg, _, client, code := openAPI(configPath, stderr, getenv)
	if code != 0 {
		return code
	}
	accountID, ok := cfg.Inboxes[fs.Arg(0)]
	if !ok {
		writef(stderr, "message delete: %s is not in the config. Create it with inbai inbox create.\n", fs.Arg(0))
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := client.deleteMessage(ctx, accountID, fs.Arg(1)); err != nil {
		writef(stderr, "message delete: %s\n", err)
		return 1
	}
	emit(stdout, *asJSON, "Deleted "+fs.Arg(1)+"\n", map[string]string{"deleted": fs.Arg(1)})
	return 0
}

func readAttach(path string, stderr io.Writer) ([]apiFile, int) {
	if path == "" {
		return nil, 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writef(stderr, "message send: %s\n", err)
		return nil, 1
	}
	return []apiFile{{
		Name: filepath.Base(path),
		Type: mime.TypeByExtension(filepath.Ext(path)),
		Data: data,
	}}, 0
}

func openAPI(configPath *string, stderr io.Writer, getenv func(string) string) (config.File, string, apiClient, int) {
	path, err := configFilePath(*configPath)
	if err != nil {
		writef(stderr, "config: %s\n", err)
		return config.File{}, "", apiClient{}, 1
	}
	cfg, err := readConfig(path)
	if err != nil {
		writef(stderr, "read config: %s\n", err)
		return config.File{}, "", apiClient{}, 1
	}
	token := ""
	if getenv != nil {
		token = getenv("INBAI_OPERATOR_TOKEN")
	}
	if token == "" {
		writef(stderr, "INBAI_OPERATOR_TOKEN is required. It is the bearer token inbaid serve expects.\n")
		return config.File{}, "", apiClient{}, 2
	}
	base := cfg.APIURL
	if base == "" {
		base = config.DefaultAPIURL
	}
	return cfg, path, apiClient{base: base, token: token}, 0
}

func emit(stdout io.Writer, asJSON bool, text string, value any) {
	if !asJSON {
		writef(stdout, "%s", text)
		return
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
}
