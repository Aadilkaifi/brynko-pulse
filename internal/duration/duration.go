// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package duration parses the duration format used in configuration:
// 500ms, 30s, 5m, 2h, 1d, or combinations such as 1h30m.
package duration

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var part = regexp.MustCompile(`([0-9]+)(ms|s|m|h|d)`)

var units = map[string]time.Duration{
	"ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour,
}

// Parse reads a duration. The empty string is zero.
// Format writes d the way people write a duration: 15m, 1h, 1h30m, 45s, 0s (not 15m0s or 1h0m0s).
func Format(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func Parse(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	matches := part.FindAllStringSubmatchIndex(s, -1)
	var total time.Duration
	end := 0
	for _, p := range matches {
		if p[0] != end {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		n, err := strconv.ParseInt(s[p[2]:p[3]], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		total += time.Duration(n) * units[s[p[4]:p[5]]]
		end = p[1]
	}
	if len(matches) == 0 || end != len(s) {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return total, nil
}
