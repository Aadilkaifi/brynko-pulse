// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
)

// DefaultRelayURL is where the brynko relay takes alerts.
const DefaultRelayURL = "https://cloudagent.brynko.in/api/v1/pulse/alert"

// Errors the relay answers with, worded for status and logs.
var (
	ErrRelayKey     = errors.New("the brynko relay did not accept the key: set up the channel again from the app, or check api_key")
	ErrRelayCredits = errors.New("the brynko relay sent nothing: the account has no credits left for messages")
)

// brynko hands the alert to the brynko relay, which sends it by WhatsApp and
// email from brynko's own accounts and charges the brynko account per message.
// The relay receives the alert itself and the addresses, nothing else.
type brynko struct {
	client          *http.Client
	url, key        string
	email, whatsapp []string
}

type relayAlert struct {
	Alert    alert.Alert `json:"alert"`
	Subject  string      `json:"subject"`
	Email    []string    `json:"email"`
	WhatsApp []string    `json:"whatsapp"`
}

func (b *brynko) Send(ctx context.Context, a alert.Alert) error {
	if b.key == "" {
		return errors.New("api_key is empty: set its secret in secrets.env")
	}
	body, err := json.Marshal(relayAlert{Alert: a, Subject: a.Subject(), Email: nonNil(b.email), WhatsApp: nonNil(b.whatsapp)})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid relay URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.key)
	req.Header.Set("User-Agent", "brynko-pulse")
	resp, err := b.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusNotFound:
		return ErrRelayKey
	case resp.StatusCode == http.StatusPaymentRequired:
		return ErrRelayCredits
	}
	return fmt.Errorf("the brynko relay answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}
