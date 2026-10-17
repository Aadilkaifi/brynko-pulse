// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package alert is the message pulse sends, and how it reads to people.
package alert

import (
	"bytes"
	"fmt"
	"html/template"
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
	// Self-healing: it failed and pulse's fix made it work again; or pulse tried
	// as often as it may and it still fails.
	KindHealed     = "healed"
	KindHealFailed = "heal_failed"
)

// Alert is one notification. Its JSON form is what webhooks receive.
type Alert struct {
	Server        string `json:"server"`
	ServerAddress string `json:"server_address,omitempty"` // how people reach the server, when configured
	Monitor       string `json:"monitor"`
	MonitorName   string `json:"monitor_name"`
	MonitorType   string `json:"monitor_type"`
	TypeLabel     string `json:"type_label,omitempty"` // the type in words, such as "Disk"
	Kind          string `json:"kind"`
	Severity      string `json:"severity"`
	Detail        string `json:"detail"`
	Condition     string `json:"condition,omitempty"`
	// Rule is the condition in words, such as "used space is above 85%".
	Rule     string        `json:"rule,omitempty"`
	Since    time.Time     `json:"since,omitzero"`
	At       time.Time     `json:"at"`
	Duration time.Duration `json:"duration_ns,omitempty"`
	// Heal says what self-healing did about it, in words, when it did something.
	Heal string `json:"heal,omitempty"`
}

var severityRank = map[string]int{"info": 0, "warning": 1, "critical": 2}

// AtLeast reports whether sev is at or above min.
func AtLeast(sev, min string) bool {
	return severityRank[sev] >= severityRank[min]
}

// state is the word an alert leads with.
func (a Alert) state() string {
	switch a.Kind {
	case KindResolved:
		return "Resolved"
	case KindRepeat:
		return "Still failing"
	case KindSummary:
		return "Many alerts"
	case KindTest:
		return "Test"
	case KindFlapping:
		return "Flapping"
	case KindSettled:
		return "Settled"
	case KindHealed:
		return "Fixed"
	case KindHealFailed:
		return "Could not fix"
	}
	if a.Severity == "" {
		return "Alert"
	}
	return strings.ToUpper(a.Severity[:1]) + a.Severity[1:]
}

// Headline is what happened, in one line: "disk-data: Used space: 91.2%".
func (a Alert) Headline() string {
	switch a.Kind {
	case KindResolved:
		return fmt.Sprintf("%s is back to normal after %s", a.MonitorName, Human(a.Duration))
	case KindHealed:
		return fmt.Sprintf("%s was fixed by pulse after %s", a.MonitorName, Human(a.Duration))
	case KindSummary, KindTest, KindFlapping, KindSettled:
		if a.MonitorName != "" && a.Kind != KindSummary && a.Kind != KindTest {
			return fmt.Sprintf("%s is %s", a.MonitorName, strings.ToLower(a.state()))
		}
		return a.Detail
	}
	if a.Detail == "" {
		return a.MonitorName
	}
	return a.MonitorName + ": " + a.Detail
}

// Subject is a one-line summary, used as an email subject: the state first, then
// the server and what happened, so a list of alerts reads at a glance.
func (a Alert) Subject() string {
	switch a.Kind {
	case KindSummary:
		return fmt.Sprintf("[Many alerts] %s", a.Server)
	case KindTest:
		return fmt.Sprintf("[Test] %s: alerts reach you", a.Server)
	}
	return fmt.Sprintf("[%s] %s: %s", a.state(), a.Server, a.Headline())
}

// fact is one labelled line of the message.
type fact struct{ Label, Value string }

func (a Alert) facts() []fact {
	server := a.Server
	if a.ServerAddress != "" {
		server += " (" + a.ServerAddress + ")"
	}
	out := []fact{{"Server", server}}
	if a.Monitor != "" && a.Kind != KindSummary && a.Kind != KindTest {
		mon := a.Monitor
		if a.TypeLabel != "" {
			mon += " (" + a.TypeLabel + ")"
		}
		out = append(out, fact{"Monitor", mon})
	}
	if !a.Since.IsZero() && a.Kind != KindResolved && a.Kind != KindHealed {
		out = append(out, fact{"Started", fmt.Sprintf("%s, %s ago", a.Since.Local().Format("2 Jan 2006, 15:04 MST"),
			Human(a.At.Sub(a.Since)))})
	}
	if (a.Kind == KindResolved || a.Kind == KindHealed) && a.Detail != "" {
		out = append(out, fact{"Now", a.Detail})
	}
	if a.Heal != "" {
		out = append(out, fact{"Healing", a.Heal})
	}
	if a.Severity != "" && a.Kind != KindResolved && a.Kind != KindHealed && a.Kind != KindTest {
		out = append(out, fact{"Severity", a.Severity})
	}
	return out
}

func (a Alert) rule() string {
	if a.Rule != "" {
		return "Alerts when " + a.Rule + "."
	}
	if a.Condition != "" {
		return "Alert rule: " + a.Condition
	}
	return ""
}

// Text is the full message in plain text.
func (a Alert) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s on %s\n\n%s\n", strings.ToUpper(a.state()), a.Server, a.Headline())
	switch a.Kind {
	case KindFiring, KindRepeat, KindHealFailed, "":
		if r := a.rule(); r != "" {
			b.WriteString(r + "\n")
		}
	case KindFlapping, KindSettled:
		b.WriteString(a.Detail + "\n")
	}
	b.WriteString("\n")
	for _, f := range a.facts() {
		fmt.Fprintf(&b, "%-9s %s\n", f.Label, f.Value)
	}
	return b.String()
}

// colour is the band at the top of the email: the severity, or green when it is over.
func (a Alert) colour() string {
	switch a.Kind {
	case KindResolved, KindSettled, KindHealed:
		return "#1a7f37"
	case KindTest, KindSummary:
		return "#5f6368"
	case KindHealFailed:
		return "#c0392b"
	}
	switch a.Severity {
	case "critical":
		return "#c0392b"
	case "info":
		return "#2f6fd8"
	}
	return "#b7791f"
}

var htmlTmpl = template.Must(template.New("alert").Parse(`<!doctype html>
<html><body style="margin:0;padding:24px;background:#f3f4f6;font-family:Segoe UI,Helvetica,Arial,sans-serif;color:#1a1a1a">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;margin:0 auto;background:#ffffff;border:1px solid #e3e5e8;border-radius:8px">
<tr><td style="height:6px;background:{{.Colour}};border-radius:8px 8px 0 0"></td></tr>
<tr><td style="padding:20px 24px 4px">
<div style="font-size:12px;font-weight:600;letter-spacing:.04em;color:{{.Colour}};text-transform:uppercase">{{.State}} &middot; {{.Server}}</div>
<div style="font-size:18px;font-weight:600;margin-top:6px;line-height:1.35">{{.Headline}}</div>
{{if .Rule}}<div style="font-size:13px;color:#5f6368;margin-top:6px">{{.Rule}}</div>{{end}}
{{if .Note}}<div style="font-size:13px;color:#5f6368;margin-top:6px">{{.Note}}</div>{{end}}
</td></tr>
<tr><td style="padding:14px 24px 22px">
<table role="presentation" cellpadding="0" cellspacing="0" style="font-size:13px;line-height:1.6">
{{range .Facts}}<tr><td style="color:#5f6368;padding-right:18px;vertical-align:top">{{.Label}}</td><td>{{.Value}}</td></tr>{{end}}
</table></td></tr>
<tr><td style="padding:12px 24px;border-top:1px solid #e3e5e8;font-size:11px;color:#8a8f98">Sent by brynko-pulse on {{.Server}}.</td></tr>
</table></body></html>
`))

// HTML is the message as a small, self-contained HTML email: a colour band for the
// severity, the headline, the rule in words and the facts. No images, no links to load.
func (a Alert) HTML() string {
	data := struct {
		Colour, State, Server, Headline, Rule, Note string
		Facts                                       []fact
	}{Colour: a.colour(), State: a.state(), Server: a.Server, Headline: a.Headline(), Facts: a.facts()}
	switch a.Kind {
	case KindFiring, KindRepeat, KindHealFailed, "":
		data.Rule = a.rule()
	case KindFlapping, KindSettled:
		data.Note = a.Detail
	}
	var b bytes.Buffer
	if err := htmlTmpl.Execute(&b, data); err != nil {
		return "<pre>" + template.HTMLEscapeString(a.Text()) + "</pre>"
	}
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
