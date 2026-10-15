// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package alert is the message pulse sends, and how it reads to people.
package alert

import (
	"fmt"
	"strings"
	"time"
)

// Kinds of alert.
const (
	KindFiring   = "firing"
	KindRepeat   = "repeat"
	KindResolved = "resolved"
	KindSummary  = "summary"
	KindTest     = "test"
	KindFlapping = "flapping"
	KindSettled  = "settled"
)

// Alert is one notification. Its JSON form is what webhooks receive.
type Alert struct {
	Server      string        `json:"server"`
	Monitor     string        `json:"monitor"`
	MonitorName string        `json:"monitor_name"`
	MonitorType string        `json:"monitor_type"`
	Kind        string        `json:"kind"`
	Severity    string        `json:"severity"`
	Detail      string        `json:"detail"`
	Condition   string        `json:"condition,omitempty"`
	Since       time.Time     `json:"since,omitempty"`
	At          time.Time     `json:"at"`
	Duration    time.Duration `json:"duration_ns,omitempty"`
}

var severityRank = map[string]int{"info": 0, "warning": 1, "critical": 2}

// AtLeast reports whether sev is at or above min.
func AtLeast(sev, min string) bool {
	return severityRank[sev] >= severityRank[min]
}

// Subject is a one-line summary, used as an email subject.
func (a Alert) Subject() string {
	name := a.MonitorName
	switch a.Kind {
	case KindResolved:
		return fmt.Sprintf("Resolved on %s: %s", a.Server, name)
	case KindRepeat:
		return fmt.Sprintf("Still failing on %s: %s", a.Server, name)
	case KindSummary:
		return fmt.Sprintf("Many alerts on %s", a.Server)
	case KindTest:
		return fmt.Sprintf("Test alert from %s", a.Server)
	case KindFlapping:
		return fmt.Sprintf("Flapping on %s: %s", a.Server, name)
	case KindSettled:
		return fmt.Sprintf("Settled on %s: %s", a.Server, name)
	default:
		return fmt.Sprintf("%s on %s: %s", strings.ToUpper(a.Severity), a.Server, name)
	}
}

// Text is the full message in plain text.
func (a Alert) Text() string {
	var b strings.Builder
	b.WriteString(a.Subject())
	b.WriteString("\n\n")
	switch a.Kind {
	case KindResolved:
		fmt.Fprintf(&b, "%s is back to normal after %s.\nNow: %s\n", a.MonitorName, Human(a.Duration), a.Detail)
	case KindSummary, KindTest, KindFlapping, KindSettled:
		b.WriteString(a.Detail + "\n")
	default:
		fmt.Fprintf(&b, "%s\n", a.Detail)
		if a.Condition != "" {
			fmt.Fprintf(&b, "Alert rule: %s\n", a.Condition)
		}
		if !a.Since.IsZero() {
			fmt.Fprintf(&b, "Since: %s (%s)\n", a.Since.UTC().Format("2006-01-02 15:04 MST"), Human(a.At.Sub(a.Since)))
		}
	}
	fmt.Fprintf(&b, "\nServer: %s\nMonitor: %s (%s)\n", a.Server, a.Monitor, a.MonitorType)
	return b.String()
}

// Human writes a duration the way people say it: 45s, 14m, 2h 5m, 3d 4h.
func Human(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h, m := int(d.Hours()), int(d.Minutes())%60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		days, h := int(d.Hours())/24, int(d.Hours())%24
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, h)
	}
}
