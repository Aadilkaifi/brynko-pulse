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
	a := Alert{Server: "web-1", ServerAddress: "203.0.113.10", Monitor: "root-disk", MonitorName: "root-disk",
		MonitorType: "disk", TypeLabel: "Disk", Kind: KindFiring, Severity: "critical", Detail: "Used space: 91%",
		Condition: "used_percent > 85", Rule: "used space is above 85%", Since: at.Add(-14 * time.Minute), At: at}
	if got := a.Subject(); got != "[Critical] web-1: root-disk: Used space: 91%" {
		t.Errorf("subject = %q", got)
	}
	for _, want := range []string{"CRITICAL on web-1", "root-disk: Used space: 91%", "Alerts when used space is above 85%.",
		"web-1 (203.0.113.10)", "root-disk (Disk)", "14m ago"} {
		if !strings.Contains(a.Text(), want) {
			t.Errorf("text missing %q:\n%s", want, a.Text())
		}
	}
	html := a.HTML()
	for _, want := range []string{"#c0392b", "Critical &middot; web-1", "Alerts when used space is above 85%.", "203.0.113.10"} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q", want)
		}
	}
	a.Kind, a.Duration, a.Detail = KindResolved, 14*time.Minute, "Used space: 70%"
	if !strings.Contains(a.Text(), "root-disk is back to normal after 14m") || !strings.Contains(a.Text(), "Now       Used space: 70%") {
		t.Errorf("resolved text:\n%s", a.Text())
	}
	if a.Subject() != "[Resolved] web-1: root-disk is back to normal after 14m" || !strings.Contains(a.HTML(), "#1a7f37") {
		t.Errorf("resolved subject %q", a.Subject())
	}
}

// What a server sends can reach an HTML email; it must arrive as text, never as markup.
func TestHTMLEscapesWhatChecksReport(t *testing.T) {
	a := Alert{Server: "web-1", MonitorName: "site", Kind: KindFiring, Severity: "warning",
		Detail: `Error: <script>alert(1)</script>`}
	if html := a.HTML(); strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("detail was not escaped:\n%s", html)
	}
}

func TestAtLeast(t *testing.T) {
	if !AtLeast("critical", "warning") || AtLeast("info", "critical") || !AtLeast("warning", "warning") {
		t.Fatal("severity ordering is wrong")
	}
}

// Self-healing messages say what broke, what pulse did and how it ended.
func TestHealMessages(t *testing.T) {
	at := time.Date(2026, 10, 17, 10, 5, 0, 0, time.UTC)
	healed := Alert{Server: "web-1", Monitor: "web", MonitorName: "nginx", Kind: KindHealed, Severity: "warning",
		Detail: "Running", At: at, Duration: 2 * time.Minute,
		Heal: "It failed (stopped). pulse restarted nginx at 10:03 and it works again."}
	if got := healed.Subject(); got != "[Fixed] web-1: nginx was fixed by pulse after 2m" {
		t.Errorf("subject = %q", got)
	}
	text := healed.Text()
	for _, want := range []string{"Healing   It failed (stopped). pulse restarted nginx at 10:03", "Now       Running"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Severity") || healed.colour() != "#1a7f37" {
		t.Errorf("a fixed problem reads as good news:\n%s", text)
	}

	failed := Alert{Server: "web-1", Monitor: "web", MonitorName: "nginx", Kind: KindHealFailed, Severity: "critical",
		Detail: "stopped", Rule: "the service is not running", At: at, Since: at.Add(-10 * time.Minute),
		Heal: "pulse tried to restart nginx 3 times in 1h and it still fails."}
	if got := failed.Subject(); got != "[Could not fix] web-1: nginx: stopped" {
		t.Errorf("subject = %q", got)
	}
	if !strings.Contains(failed.Text(), "Alerts when the service is not running.") || failed.colour() != "#c0392b" ||
		!strings.Contains(failed.HTML(), "tried to restart nginx 3 times") {
		t.Errorf("give-up message:\n%s", failed.Text())
	}
}
