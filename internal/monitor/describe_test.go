// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"testing"

	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
)

func mustParse(t *testing.T, s string) rule.Condition {
	t.Helper()
	c, err := rule.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// People read words, not 0 and 1: what status lists, test results and alerts show.
func TestDescribeUsesTheCatalogsWords(t *testing.T) {
	cases := []struct {
		typ, cond string
		values    rule.Values
		want      string
	}{
		{"container", "running == 0", rule.Values{"running": 1.0}, "Running"},
		{"container", "running == 0", rule.Values{"running": 0.0}, "Not running"},
		{"service", "active == 0", rule.Values{"active": false}, "Not running"},
		{"http", "ok == 0", rule.Values{"ok": false, "status": 502.0}, "Not answering as expected (status code 502)"},
		{"disk", "used_percent > 85", rule.Values{"used_percent": 91.24}, "Used space: 91.2%"},
		{"http", "latency_ms > 800", rule.Values{"latency_ms": 840.0}, "Response time: 840 ms"},
		{"file", "age > 26h", rule.Values{"age": 93600.0}, "Time since it changed: 1d 2h"},
		{"memory", "available_bytes < 1", rule.Values{"available_bytes": 1610612736.0}, "Available: 1.5 GB"},
		{"container", `state == "exited"`, rule.Values{"state": "exited"}, "State: exited"},
		{"http", `json_value != "ok"`, rule.Values{"json_value": "degraded"}, "JSON field value: degraded"},
	}
	for _, c := range cases {
		if got := Describe(c.typ, mustParse(t, c.cond), c.values); got != c.want {
			t.Errorf("%s %s: got %q, want %q", c.typ, c.cond, got, c.want)
		}
	}
}

func TestDescribeConditionReadsAsASentence(t *testing.T) {
	for cond, want := range map[string]string{
		"used_percent > 85":  "used space is above 85%",
		"running == 0":       "not running",
		"late == 1":          "late",
		"days_left < 14":     "days until it expires is below 14 days",
		"age >= 26h":         "time since it changed is at least 1d 2h",
		`state != "running"`: `state is not "running"`,
	} {
		typ := map[string]string{"used_percent > 85": "disk", "running == 0": "container", "late == 1": "heartbeat",
			"days_left < 14": "ssl", "age >= 26h": "file", `state != "running"`: "container"}[cond]
		if got := DescribeCondition(typ, mustParse(t, cond)); got != want {
			t.Errorf("%s: got %q, want %q", cond, got, want)
		}
	}
}

func TestJSONPathReadsOneField(t *testing.T) {
	body := []byte(`{"status":"ok","data":{"count":42,"up":true},"items":[{"name":"a"},{"name":"b"}],"none":null}`)
	cases := []struct {
		path  []string
		want  any
		found bool
	}{
		{[]string{"status"}, "ok", true},
		{[]string{"data", "count"}, 42.0, true},
		{[]string{"data", "up"}, "true", true},
		{[]string{"items", "1", "name"}, "b", true},
		{[]string{"none"}, "null", true},
		{[]string{"items", "7"}, nil, false},
		{[]string{"missing"}, nil, false},
		{[]string{"status", "deeper"}, nil, false},
	}
	for _, c := range cases {
		got, found := jsonAt(body, c.path)
		if found != c.found || got != c.want {
			t.Errorf("%v: got %v, %v; want %v, %v", c.path, got, found, c.want, c.found)
		}
	}
	if _, found := jsonAt([]byte("<html>not json</html>"), []string{"status"}); found {
		t.Error("a body that is not JSON has no field")
	}
}

func TestDescribeSaysWhyItIsNotAnswering(t *testing.T) {
	c, err := rule.Parse("ok == 0")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		values rule.Values
		want   string
	}{
		{rule.Values{"ok": false, "status": 502.0}, "Not answering as expected (status code 502)"},
		{rule.Values{"ok": false, "status": 200.0, "json_found": true, "json_value": "degraded", "json_match": false},
			`Not answering as expected (status code 200, JSON field is "degraded")`},
		{rule.Values{"ok": false, "status": 200.0, "body_match": false}, "Not answering as expected (status code 200, text not found)"},
		{rule.Values{"ok": false, "status": 200.0, "json_found": false, "json_match": false},
			"Not answering as expected (status code 200, JSON field not found)"},
		{rule.Values{"ok": true, "status": 200.0, "json_match": true}, "Answering as expected (status code 200)"},
		{rule.Values{"ok": true, "status": 200.0, "json_found": true, "json_value": "ok", "json_match": true},
			`Answering as expected (status code 200, JSON field is "ok")`},
	}
	for _, tc := range tests {
		if got := Describe("http", c, tc.values); got != tc.want {
			t.Errorf("got  %s\nwant %s", got, tc.want)
		}
	}
}
