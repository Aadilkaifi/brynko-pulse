// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"reflect"
	"testing"
)

func TestWatchdogRecipientsFollowTheMedia(t *testing.T) {
	cfg := &Config{
		Watchdog: &Watchdog{Key: "${K}", Notify: []string{"team"}},
		Channels: map[string]Channel{"team": {Type: "brynko", Email: []string{"ops@example.com"}, WhatsApp: []string{"+919876543210"}}},
	}
	emails, phones, _ := cfg.WatchdogRecipients()
	if !reflect.DeepEqual(emails, []string{"ops@example.com"}) || !reflect.DeepEqual(phones, []string{"+919876543210"}) {
		t.Fatalf("both by default: %v %v", emails, phones)
	}
	cfg.Watchdog.Media = []string{"email"}
	emails, phones, _ = cfg.WatchdogRecipients()
	if len(emails) != 1 || phones != nil {
		t.Fatalf("email only: %v %v", emails, phones)
	}
	cfg.Watchdog.Media = []string{"whatsapp"}
	emails, phones, _ = cfg.WatchdogRecipients()
	if emails != nil || len(phones) != 1 {
		t.Fatalf("WhatsApp only: %v %v", emails, phones)
	}
}

func TestWatchdogGoesThroughEveryRelayChannelAndNothingElse(t *testing.T) {
	cfg := &Config{
		Watchdog: &Watchdog{Key: "${K}"},
		Defaults: Defaults{Notify: []string{"mail"}},
		Channels: map[string]Channel{
			"mail": {Type: "smtp", To: []string{"smtp@example.com"}},
			"b":    {Type: "brynko", Email: []string{"b@example.com"}},
			"a":    {Type: "brynko", WhatsApp: []string{"+911234567890"}},
		},
	}
	emails, phones, skipped := cfg.WatchdogRecipients()
	if !reflect.DeepEqual(emails, []string{"b@example.com"}) || !reflect.DeepEqual(phones, []string{"+911234567890"}) || skipped != nil {
		t.Fatalf("naming none: every relay channel, never the default email channel: %v %v %v", emails, phones, skipped)
	}
	cfg.Watchdog.Notify = []string{"mail", "b"}
	emails, _, skipped = cfg.WatchdogRecipients()
	if !reflect.DeepEqual(emails, []string{"b@example.com"}) || !reflect.DeepEqual(skipped, []string{"mail"}) {
		t.Fatalf("an email channel named is skipped: %v %v", emails, skipped)
	}
}
