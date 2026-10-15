// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package channel delivers alerts. Every channel implements Sender.
package channel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
)

// ErrNotAvailable means the channel type is not available in this version.
var ErrNotAvailable = errors.New("not available in this version yet")

// Sender delivers one alert.
type Sender interface {
	Send(ctx context.Context, a alert.Alert) error
}

// New builds the sender for one configured channel. Secrets are resolved here,
// once, and never logged.
func New(c config.Channel, secrets config.Secrets, client *http.Client) (Sender, error) {
	secret := secrets.Resolve
	switch c.Type {
	case "smtp":
		return newSMTP(c, secret(c.Password)), nil
	case "telegram":
		return &telegram{client: client, token: secret(c.BotToken), chatID: c.ChatID,
			api: "https://api.telegram.org"}, nil
	case "slack":
		return &slack{client: client, url: secret(c.WebhookURL)}, nil
	case "webhook":
		return &webhook{client: client, url: c.URL, secret: secret(c.Secret)}, nil
	case "whatsapp":
		return &whatsapp{client: client, api: "https://graph.facebook.com/v20.0", phoneID: c.PhoneNumberID,
			token: secret(c.AccessToken), template: c.Template, language: or(c.Language, "en"), to: c.To}, nil
	case "brynko":
		return nil, fmt.Errorf("%s channels: %w", c.Type, ErrNotAvailable)
	default:
		return nil, fmt.Errorf("unknown channel type %q", c.Type)
	}
}

// NewHTTPClient is the client channels share: bounded time, proxy from the environment.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second},
	}
}

// postJSON sends body and checks for a 2xx answer. Errors never include the URL,
// because for Telegram and Slack the URL contains the secret.
func postJSON(ctx context.Context, client *http.Client, rawURL string, body []byte, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "brynko-pulse")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return nil
}

type telegram struct {
	client        *http.Client
	token, chatID string
	api           string
}

func (t *telegram) Send(ctx context.Context, a alert.Alert) error {
	if t.token == "" {
		return errors.New("bot_token is empty: set its environment variable")
	}
	body, _ := json.Marshal(map[string]string{"chat_id": t.chatID, "text": a.Text()})
	return postJSON(ctx, t.client, t.api+"/bot"+t.token+"/sendMessage", body, nil)
}

type slack struct {
	client *http.Client
	url    string
}

func (s *slack) Send(ctx context.Context, a alert.Alert) error {
	if s.url == "" {
		return errors.New("webhook_url is empty: set its environment variable")
	}
	body, _ := json.Marshal(map[string]string{"text": a.Text()})
	return postJSON(ctx, s.client, s.url, body, nil)
}

type webhook struct {
	client      *http.Client
	url, secret string
}

// Send posts the alert as JSON. With a secret, X-Pulse-Signature is the
// hex HMAC-SHA256 of "<timestamp>.<body>", so receivers can reject forgeries
// and replays.
func (w *webhook) Send(ctx context.Context, a alert.Alert) error {
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	headers := map[string]string{}
	if w.secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		headers["X-Pulse-Timestamp"] = ts
		headers["X-Pulse-Signature"] = "sha256=" + Sign(w.secret, ts, body)
	}
	return postJSON(ctx, w.client, w.url, body, headers)
}

// Sign computes the webhook signature.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// whatsapp sends through the user's own Meta WhatsApp Cloud API account.
// Alerts start the conversation, which WhatsApp allows only with an approved
// template; it must have two body variables: {{1}} the subject, {{2}} the detail.
type whatsapp struct {
	client              *http.Client
	api, phoneID, token string
	template, language  string
	to                  []string
}

func (w *whatsapp) Send(ctx context.Context, a alert.Alert) error {
	if w.token == "" {
		return errors.New("access_token is empty: add it to secrets.env")
	}
	var failed []string
	for _, to := range w.to {
		body, _ := json.Marshal(map[string]any{
			"messaging_product": "whatsapp",
			"to":                strings.TrimPrefix(to, "+"),
			"type":              "template",
			"template": map[string]any{
				"name":     w.template,
				"language": map[string]string{"code": w.language},
				"components": []any{map[string]any{"type": "body", "parameters": []any{
					map[string]string{"type": "text", "text": templateText(a.Subject(), 200)},
					map[string]string{"type": "text", "text": templateText(a.Detail, 900)},
				}}},
			},
		})
		err := postJSON(ctx, w.client, w.api+"/"+url.PathEscape(w.phoneID)+"/messages", body,
			map[string]string{"Authorization": "Bearer " + w.token})
		if err != nil {
			failed = append(failed, to+": "+err.Error())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("whatsapp to %s", strings.Join(failed, "; "))
	}
	return nil
}

// templateText fits WhatsApp's rules for template variables: no new lines or
// tabs, no more than four spaces in a row, and a length limit.
func templateText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		s = "-"
	}
	if r := []rune(s); len(r) > max {
		s = string(r[:max-3]) + "..."
	}
	return s
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
