// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package rule

import (
	"strings"
	"testing"
)

func TestParseAndEval(t *testing.T) {
	v := Values{"used_percent": 91.5, "age": 100000.0, "state": "failed", "active": false, "count": 0}
	tests := []struct {
		cond string
		want bool
	}{
		{"used_percent > 85", true},
		{"used_percent>=91.5", true},
		{"used_percent < 85", false},
		{"age > 26h", true},
		{"age > 1d", true},
		{"age > 2d", false},
		{"age > 1h30m", true},
		{`state != "running"`, true},
		{"state == failed", true},
		{"active == 0", true},
		{"count == 0", true},
		{"count != 0", false},
	}
	for _, tc := range tests {
		c, err := Parse(tc.cond)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.cond, err)
		}
		got, err := c.Eval(v)
		if err != nil {
			t.Fatalf("Eval(%q): %v", tc.cond, err)
		}
		if got != tc.want {
			t.Errorf("%q = %v, want %v", tc.cond, got, tc.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"used percent high", "x > ", "state > running", "x > 1e999", "> 5"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) should fail", s)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	c, _ := Parse("free_bytes < 10")
	if _, err := c.Eval(Values{"used_percent": 1.0}); err == nil || !strings.Contains(err.Error(), "available: used_percent") {
		t.Fatalf("missing metric error = %v", err)
	}
	c, _ = Parse("state > 1")
	if _, err := c.Eval(Values{"state": "running"}); err == nil {
		t.Fatal("comparing text with a number should fail")
	}
}

func TestFormat(t *testing.T) {
	for in, want := range map[any]string{91.0: "91", 91.25: "91.2", true: "1", "ok": "ok", 3: "3"} {
		if got := Format(in); got != want {
			t.Errorf("Format(%v) = %q, want %q", in, got, want)
		}
	}
}
