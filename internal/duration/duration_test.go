// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package duration

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	ok := map[string]time.Duration{
		"":      0,
		"500ms": 500 * time.Millisecond,
		"30s":   30 * time.Second,
		"5m":    5 * time.Minute,
		"2h":    2 * time.Hour,
		"1d":    24 * time.Hour,
		"1h30m": 90 * time.Minute,
		"1d2h":  26 * time.Hour,
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"5", "m", "5x", "1h 30m", "-5s", "5s!"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestFormat(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0s", 30 * time.Second: "30s", 15 * time.Minute: "15m", time.Hour: "1h",
		90 * time.Minute: "1h30m", 26 * time.Hour: "26h", 500 * time.Millisecond: "500ms", 90 * time.Second: "1m30s",
	} {
		if got := Format(d); got != want {
			t.Errorf("Format(%v) = %q, want %q", d, got, want)
		}
	}
}
