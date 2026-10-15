// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"strings"
	"testing"
	"time"
)

func TestHuman(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Second:            "45s",
		14 * time.Minute:            "14m",
		2*time.Hour + 5*time.Minute: "2h 5m",
		3 * time.Hour:               "3h",
		76 * time.Hour:              "3d 4h",
		48 * time.Hour:              "2d",
	} {
		if got := Human(d); got != want {
			t.Errorf("Human(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestMessages(t *testing.T) {
	at := time.Date(2026, 1, 1, 14, 30, 0, 0, time.UTC)
	a := Alert{Server: "web-1", Monitor: "root-disk", MonitorName: "root-disk", MonitorType: "disk",
		Kind: KindFiring, Severity: "critical", Detail: "used_percent is 91",
		Condition: "used_percent > 85", Since: at.Add(-14 * time.Minute), At: at}
	if got := a.Subject(); got != "CRITICAL on web-1: root-disk" {
		t.Errorf("subject = %q", got)
	}
	for _, want := range []string{"used_percent is 91", "Alert rule: used_percent > 85", "(14m)"} {
		if !strings.Contains(a.Text(), want) {
			t.Errorf("text missing %q:\n%s", want, a.Text())
		}
	}
	a.Kind, a.Duration = KindResolved, 14*time.Minute
	if !strings.Contains(a.Text(), "back to normal after 14m") {
		t.Errorf("resolved text:\n%s", a.Text())
	}
}

func TestAtLeast(t *testing.T) {
	if !AtLeast("critical", "warning") || AtLeast("info", "critical") || !AtLeast("warning", "warning") {
		t.Fatal("severity ordering is wrong")
	}
}
