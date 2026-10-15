// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package quickadd

import (
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		spec   Spec
		id     string
		has    []string
		hasNot []string
	}{
		{Spec{Type: "disk", Target: "/data", Above: f(85)}, "disk-data",
			[]string{"path: /data", "alert_when: used_percent > 85", "clear_when: used_percent < 80"}, nil},
		{Spec{Type: "disk", Target: "/"}, "disk-root", []string{"used_percent > 85"}, nil},
		{Spec{Type: "disk", Target: `C:\`}, "disk-root", []string{`path: C:\`}, nil},
		{Spec{Type: "memory"}, "memory", []string{"used_percent > 90", "used_percent < 85"}, nil},
		{Spec{Type: "load", Above: f(1.5)}, "load", []string{"per_cpu > 1.5"}, []string{"clear_when"}},
		{Spec{Type: "service", Target: "nginx", Notify: []string{"a", "b"}}, "service-nginx",
			[]string{"service: nginx", "notify: [a, b]"}, []string{"alert_when"}},
		{Spec{Type: "port", Target: "db.local:5432"}, "port-5432", []string{"host: db.local", "port: 5432"}, nil},
		{Spec{Type: "port", Target: "80"}, "port-80", []string{"port: 80"}, []string{"host:"}},
		{Spec{Type: "http", Target: "https://Example.com/health?token=SECRET", Status: 200}, "http-example-com-health",
			[]string{"expect_status: 200"}, nil},
		{Spec{Type: "ssl", Target: "example.com:8443", Days: 21}, "ssl-example-com", []string{"days_left < 21"}, nil},
		{Spec{Type: "file", Target: "/var/b.tar", When: "age > 1d"}, "file-var-b-tar", []string{"alert_when: age > 1d"}, nil},
		{Spec{Type: "heartbeat", ID: "nightly", Every: "1d", Grace: "1h"}, "nightly", []string{"expect_every: 1d", "grace: 1h"}, nil},
		{Spec{Type: "command", ID: "q", Run: []string{"/bin/check", "--max", "100"}}, "q",
			[]string{"- /bin/check", "- \"100\"", "alert_when: exit_code != 0"}, nil},
		{Spec{Type: "cpu", When: "used_percent > 50", For: "5m", Severity: "critical", Name: "CPU"}, "cpu",
			[]string{"alert_when: used_percent > 50", "for: 5m", "severity: critical", "name: CPU"}, []string{"clear_when"}},
	}
	for _, c := range cases {
		r, err := Build(c.spec)
		if err != nil {
			t.Errorf("%+v: %v", c.spec, err)
			continue
		}
		y := string(r.YAML)
		if r.ID != c.id || r.Path != "monitors/"+c.id+".yaml" || !strings.Contains(y, "id: "+c.id) {
			t.Errorf("%+v: id %q path %q\n%s", c.spec, r.ID, r.Path, y)
		}
		if strings.Contains(y, "SECRET") && !strings.Contains(y, "url:") {
			t.Errorf("query leaked outside the url: %s", y)
		}
		for _, h := range c.has {
			if !strings.Contains(y, h) {
				t.Errorf("%s: missing %q in\n%s", c.id, h, y)
			}
		}
		for _, h := range c.hasNot {
			if strings.Contains(y, h) {
				t.Errorf("%s: unexpected %q in\n%s", c.id, h, y)
			}
		}
	}
}

func TestBuildErrors(t *testing.T) {
	f := 50.0
	for _, s := range []Spec{
		{Type: "nope"},
		{Type: "disk"},
		{Type: "memory", Target: "x"},
		{Type: "service", Target: "x", Above: &f},
		{Type: "port", Target: "70000"},
		{Type: "port", Target: "host:"},
		{Type: "http", Target: "example.com"},
		{Type: "file", Target: "/x"},
		{Type: "heartbeat", Every: "1d"},
		{Type: "command"},
		{Type: "memory", Run: []string{"x"}},
	} {
		if _, err := Build(s); err == nil {
			t.Errorf("%+v should fail", s)
		}
	}
}

func TestMakeID(t *testing.T) {
	if got := makeID("HTTP-Example.com-" + strings.Repeat("a", 80)); len(got) > 63 || got != strings.ToLower(got) {
		t.Errorf("makeID = %q", got)
	}
	if makeID("///") != "monitor" {
		t.Error("empty hint should fall back")
	}
}
