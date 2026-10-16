// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package rule

import "testing"

func TestExpect(t *testing.T) {
	tests := []struct {
		expect string
		value  any
		met    bool
	}{
		{"== ok", "ok", true},
		{"== ok", "degraded", false},
		{"!= ok", "degraded", true},
		{"==ok", "ok", true},
		{`== "200"`, "200", true},
		{`== "200"`, 200.0, true},
		{"> 0", 12.0, true},
		{"> 0", 0.0, false},
		{"<= 100", 100.0, true},
		{"== 1.5", 1.5, true},
		{"== 0", "0", false}, // a number was expected, text came back
		{"!= 0", "busy", true},
		{"== true", "true", true}, // JSON true reads as the text "true"
		{"== null", "null", true},
	}
	for _, tc := range tests {
		e, err := ParseExpect(tc.expect)
		if err != nil {
			t.Fatalf("%q: %v", tc.expect, err)
		}
		if got := e.Met(tc.value); got != tc.met {
			t.Errorf("%q with %v: got %v, want %v", tc.expect, tc.value, got, tc.met)
		}
	}
}

func TestExpectRefuses(t *testing.T) {
	for _, s := range []string{"", "ok", "==", "> ok", "=~ x", "< \"5\""} {
		if _, err := ParseExpect(s); err == nil {
			t.Errorf("%q should be refused", s)
		}
	}
	if e, _ := ParseExpect(">=  10 "); e.String() != ">= 10" {
		t.Errorf("String: %q", e.String())
	}
}
