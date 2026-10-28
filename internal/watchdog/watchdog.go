// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package watchdog checks in with the brynko relay every minute, so the relay
// can alert people when the server goes silent: it is down, cut off from the
// network, or pulse stopped. A dead server cannot send its own alert.
//
// It is off unless pulse.yaml has a watchdog section. A check-in carries the
// server's name and address, pulse's version, how long pulse and the machine
// have been running and the email addresses and WhatsApp numbers to alert;
// nothing about monitors, values or alerts.
package watchdog

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
	"time"
)

// Every is how often pulse checks in. The relay decides how long a silence must
// last before it alerts.
const Every = time.Minute

// Checkin is what pulse tells the relay.
type Checkin struct {
	Server   string   `json:"server"`
	Address  string   `json:"address,omitempty"`
	Version  string   `json:"version"`
	Email    []string `json:"email"`
	WhatsApp []string `json:"whatsapp"`
	Every    int      `json:"every"` // seconds until the next check-in
	// Up is how many seconds pulse has been running. After a silence the relay
	// tells "pulse kept running, so the network dropped" from "pulse started
	// again". A duration rather than a start time, so a wrong server clock
	// cannot change the answer.
	Up int64 `json:"up"`
	// HostUp is how many seconds the machine has run since it started, so the
	// relay can also tell "the server restarted" from "pulse was restarted".
	// Left out when the system cannot say.
	HostUp  int64 `json:"host_up,omitempty"`
	Goodbye bool  `json:"goodbye,omitempty"`
}

// Errors the relay answers with, worded for status and logs.
var (
	ErrKey     = errors.New("the relay did not accept the key: turn the dead-server alert off and on again in the app")
	ErrCredits = errors.New("the relay holds the dead-server alert while the account has no credits")
)

// Send delivers one check-in. Errors never include the URL or the key.
func Send(ctx context.Context, client *http.Client, endpoint, key string, c Checkin) error {
	if key == "" {
		return errors.New("the relay key is empty: set its secret in secrets.env")
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid watchdog URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "brynko-pulse/"+c.Version)
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("check-in failed: %w", err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusNotFound:
		return ErrKey
	case resp.StatusCode == http.StatusPaymentRequired:
		return ErrCredits
	}
	return fmt.Errorf("the relay answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
}
