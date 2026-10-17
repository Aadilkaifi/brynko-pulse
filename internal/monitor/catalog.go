// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import "github.com/Aadilkaifi/brynko-pulse/internal/config"

// TypeInfo describes one monitor type for people and tools: what it watches, the
// values its checks produce, and the condition it uses when alert_when is not set.
// It is published in the JSON Schema (x-types), so a form can offer the right
// values for each type instead of a free-text condition, and pulse words its
// messages from it ("Not running", "Used space: 91.2%").
type TypeInfo struct {
	Type    string `json:"type"`
	Label   string `json:"label"`
	Group   string `json:"group"`
	Summary string `json:"summary"`
	// Help explains in a few sentences what the type checks and how to use it.
	Help string `json:"help,omitempty"`
	// Target is the field naming what is watched (path, service, url...), if any.
	Target string `json:"target,omitempty"`
	// Main lists the type's own fields a form shows first; the others are extra options.
	Main []string `json:"main,omitempty"`
	// Default is the condition used when alert_when is not set. Empty means alert_when is required.
	Default string `json:"default,omitempty"`
	// Starter is a sensible first condition for a type with no default.
	Starter string `json:"starter,omitempty"`
	// StarterFor is a sensible first "for" (how long a problem must last), when a type needs one.
	StarterFor string `json:"starter_for,omitempty"`
	// Extra names a value added in brackets when a flag is described ("Not answering (status 502)").
	Extra string `json:"extra,omitempty"`
	// Instant says how the type notices a change at once, between regular checks; empty when it only checks.
	Instant string `json:"instant,omitempty"`
	// Choice offers some of the type's fields as one pick, so a form shows only the chosen option's fields.
	Choice *Choice `json:"choice,omitempty"`
	// Heal lists the kinds of heal steps the type supports, from config.HealSteps; none when a fix cannot help.
	Heal []string `json:"heal,omitempty"`
	// Because lists what can make the default flag false, so a message says which ("text not found").
	Because []Reason    `json:"-"`
	OS      string      `json:"os,omitempty"` // "linux" when only Linux supports the type
	Values  []ValueInfo `json:"values"`
}

// Choice is one pick between ways to use some of a type's fields: "Check the answer:
// status only / contains text / JSON field". Fields not in the chosen option stay unset.
type Choice struct {
	Label   string         `json:"label"`
	Options []ChoiceOption `json:"options"`
}

// ChoiceOption is one way, with the fields it uses (none for "status only").
type ChoiceOption struct {
	Label  string   `json:"label"`
	Fields []string `json:"fields"`
}

// Reason is a flag that, when false, explains why the type's main flag is false.
// Value names a value to show after the text ("JSON field is \"fail\"").
type Reason struct {
	Flag, Text, Value string
}

// ValueInfo is one value a check produces, usable in alert_when and clear_when.
// A flag's label names the state it is 1 in ("Running"), so "not running" reads well.
type ValueInfo struct {
	Name string `json:"name"`
	// Kind says how to compare it: "number", "flag" (1 or 0, shown as yes or no),
	// "duration" (seconds, written as 30s, 5m, 1d), "text" (quoted), or "any"
	// (a number or text, whichever the check finds).
	Kind    string   `json:"kind"`
	Unit    string   `json:"unit,omitempty"`
	Label   string   `json:"label"`
	Help    string   `json:"help,omitempty"`
	Options []string `json:"options,omitempty"` // known values of a text value
	OS      string   `json:"os,omitempty"`      // "linux" when only Linux reports it
}

func num(name, label, unit, help string) ValueInfo {
	return ValueInfo{Name: name, Kind: "number", Unit: unit, Label: label, Help: help}
}

func flag(name, label, help string) ValueInfo {
	return ValueInfo{Name: name, Kind: "flag", Label: label, Help: help}
}

// Catalog lists every monitor type, in the order a picker shows them.
func init() {
	for i := range Catalog {
		Catalog[i].Heal = config.HealSteps(Catalog[i].Type)
	}
}

var Catalog = []TypeInfo{
	{Type: "cpu", Label: "CPU", Group: "System", Summary: "Usage across all cores, measured between two checks.",
		Help:    "Busy servers often spike for a few seconds; wait a few minutes before alerting so only lasting load wakes anyone.",
		Starter: "used_percent > 95", StarterFor: "5m",
		Values: []ValueInfo{num("used_percent", "Used", "%", "")}},
	{Type: "memory", Label: "Memory", Group: "System", Summary: "Memory in use.",
		Help:    "Memory in use by programs; the system's file cache does not count, since it is given back when needed.",
		Starter: "used_percent > 90", StarterFor: "5m",
		Values: []ValueInfo{
			num("used_percent", "Used", "%", ""),
			num("available_gb", "Available", "GB", ""),
			num("available_bytes", "Available", "bytes", ""),
			num("total_bytes", "Total", "bytes", ""),
		}},
	{Type: "disk", Label: "Disk", Group: "System", Summary: "Space on the filesystem that holds a path.",
		Help:   "Pick a filesystem (its mount point), or type any path on it.",
		Target: "path", Main: []string{"path"}, Starter: "used_percent > 85",
		Values: []ValueInfo{
			num("used_percent", "Used space", "%", ""),
			num("free_gb", "Free space", "GB", ""),
			num("free_bytes", "Free space", "bytes", ""),
			num("total_bytes", "Size", "bytes", ""),
			{Name: "inodes_used_percent", Kind: "number", Unit: "%", Label: "Inodes used", OS: "linux",
				Help: "Files and folders the filesystem can hold; it can fill up with many small files while space is left."},
		}},
	{Type: "swap", Label: "Swap", Group: "System", Summary: "Swap on Linux, the page file on Windows.",
		Starter: "used_percent > 80", StarterFor: "5m",
		Values: []ValueInfo{
			num("used_percent", "Used", "%", ""),
			num("used_bytes", "Used", "bytes", ""),
			num("total_bytes", "Total", "bytes", ""),
		}},
	{Type: "load", Label: "Load average", Group: "System", Summary: "The load average.", OS: "linux",
		Help:    "Load per core above 1 means work is waiting; a little waiting is normal, a lot for long is not.",
		Starter: "per_cpu > 2", StarterFor: "10m",
		Values: []ValueInfo{
			num("per_cpu", "Load per core", "", "The 1-minute load divided by the number of cores."),
			num("load1", "1-minute load", "", ""),
			num("load5", "5-minute load", "", ""),
			num("load15", "15-minute load", "", ""),
		}},
	{Type: "service", Label: "Service", Group: "Apps",
		Summary: "A systemd unit on Linux, a Windows Service on Windows.",
		Help:    "Alerts when the service stops. A stop or a crash is noticed the moment it happens.",
		Instant: "A stop or a crash is noticed at once (systemd on Linux, the Service Control Manager on Windows).",
		Target:  "service", Main: []string{"service"}, Default: "active == 0",
		Values: []ValueInfo{
			flag("active", "Running", "Yes while the service runs."),
			{Name: "state", Kind: "text", Label: "State", Options: []string{"running", "failed", "stopped"}},
			{Name: "restarts", Kind: "number", Label: "Restarts", OS: "linux"},
		}},
	{Type: "container", Label: "Container", Group: "Apps", Summary: "A Docker container.",
		Help:    "Alerts when the container stops. Docker events are followed, so a stop is noticed the moment it happens.",
		Instant: "A stop is noticed at once through Docker events.",
		Target:  "container", Main: []string{"container"}, Default: "running == 0", OS: "linux",
		Values: []ValueInfo{
			flag("running", "Running", "Yes while the container runs."),
			{Name: "state", Kind: "text", Label: "State",
				Options: []string{"running", "restarting", "paused", "exited", "dead", "created"}},
			num("restarts", "Restarts", "", ""),
			num("exit_code", "Exit code", "", "The last exit code."),
			flag("oom_killed", "Killed for memory", "Yes when it was stopped for running out of memory."),
			{Name: "health", Kind: "text", Label: "Health", Options: []string{"healthy", "unhealthy", "starting"},
				Help: "Only when the container has a health check."},
		}},
	{Type: "process", Label: "Process", Group: "Apps", Summary: "A running program, found by its name.",
		Help: "Counts the running processes with this name, as ps shows it (on Linux a process whose command line " +
			"contains it also counts). Alerts when none is running.",
		Target: "match", Main: []string{"match"}, Default: "count == 0",
		Values: []ValueInfo{num("count", "Running copies", "", "")}},
	{Type: "http", Label: "Website (HTTP)", Group: "Network", Summary: "A web address answers as expected.",
		Help: "pulse fetches the address. It answers as expected when the status is one you expect (any 2xx or 3xx " +
			"when you set none) and, if you choose, the answer contains your text or a JSON field in it has the value you expect.",
		Target: "url", Main: []string{"url", "expect_status", "body_contains", "json_path", "json_expect"},
		Default: "ok == 0", Extra: "status",
		Choice: &Choice{Label: "Check the answer", Options: []ChoiceOption{
			{Label: "Status only", Fields: []string{}},
			{Label: "Contains text", Fields: []string{"body_contains"}},
			{Label: "JSON field", Fields: []string{"json_path", "json_expect"}},
		}},
		Because: []Reason{
			{Flag: "body_match", Text: "text not found"},
			{Flag: "json_found", Text: "JSON field not found"},
			{Flag: "json_match", Text: "JSON field is", Value: "json_value"},
		},
		Values: []ValueInfo{
			flag("ok", "Answering as expected", "Yes when the status, and the text or JSON field you set, are as expected."),
			num("status", "Status code", "", ""),
			num("latency_ms", "Response time", "ms", ""),
			flag("body_match", "Containing the text", "Yes when the body contains body_contains. Only when it is set."),
			flag("json_found", "JSON field found", "Yes when json_path is in the response. Only when json_path is set."),
			{Name: "json_value", Kind: "any", Label: "JSON field value",
				Help: "The value at json_path, a number or text. Only when json_path is set."},
			flag("json_match", "JSON field as expected", "Yes when the JSON field matches json_expect. Only when it is set."),
			{Name: "error", Kind: "text", Label: "Error"},
		}},
	{Type: "ssl", Label: "SSL certificate", Group: "Network", Summary: "A TLS certificate, from a server or a file.",
		Help:   "Alerts before the certificate expires, so there is time to renew it.",
		Target: "host", Main: []string{"host", "file"}, Default: "days_left < 14",
		Values: []ValueInfo{
			num("days_left", "Days until it expires", "days", ""),
			flag("valid", "Valid", "Yes when the certificate is valid."),
		}},
	{Type: "port", Label: "Port", Group: "Network", Summary: "Whether a TCP port accepts connections.",
		Help: "Connects and closes at once. Use it for web servers; databases, SSH and brokers log such a connection " +
			"as a failed client, so watch them with a Service or Container monitor instead.",
		Target: "port", Main: []string{"port", "host"}, Default: "open == 0",
		Values: []ValueInfo{
			flag("open", "Accepting connections", ""),
			num("latency_ms", "Connect time", "ms", ""),
		}},
	{Type: "file", Label: "File", Group: "Files and logs", Summary: "A file's age and size.",
		Help:    "For example a nightly backup: alert when the file is older than a day and a bit.",
		Instant: "A change to the file is noticed within 2 seconds.",
		Target:  "path", Main: []string{"path"}, Starter: "age > 26h",
		Values: []ValueInfo{
			flag("exists", "Present", "Yes while the file is there."),
			{Name: "age", Kind: "duration", Label: "Time since it changed"},
			num("size_bytes", "Size", "bytes", ""),
		}},
	{Type: "log", Label: "Log", Group: "Files and logs", Summary: "Lines matching a pattern in a log.",
		Help:    "Follows a log file, the systemd journal or a Windows Event Log, and counts the lines that match.",
		Instant: "A matching line is noticed as soon as it is written.",
		Main:    []string{"source", "pattern", "window"},
		Default: "matches > 0",
		Values: []ValueInfo{
			num("matches", "Matching lines", "", "Counted over the window."),
			{Name: "last_line", Kind: "text", Label: "Last matching line"},
		}},
	{Type: "command", Label: "Command", Group: "Jobs", Summary: "Runs a program you choose.",
		Help: "Runs the program as the brynko-pulse account, never through a shell. By default it alerts when the " +
			"program exits with anything but 0. To alert on what it prints, read its output as a number or text.",
		Main: []string{"run", "parse"}, Default: "exit_code != 0",
		Values: []ValueInfo{
			num("exit_code", "Exit code", "", "0 usually means success."),
			{Name: "value", Kind: "number", Label: "Number it prints", Help: "Only when the output is read as a number."},
			{Name: "text", Kind: "text", Label: "Text it prints", Help: "Only when the output is read as text."},
		}},
	{Type: "heartbeat", Label: "Heartbeat", Group: "Jobs", Summary: "A job checks in after each run.",
		Help: "For jobs that run on a schedule, such as a nightly backup. At the end of the job, run " +
			"pulse ping <id> on this server (as root or a member of the brynko-pulse group). pulse alerts when no " +
			"ping arrives within the expected time plus the grace time.",
		Main: []string{"expect_every", "grace"}, Default: "late == 1",
		Values: []ValueInfo{
			flag("late", "Late", "Yes when no ping came within the expected time plus grace."),
			{Name: "age", Kind: "duration", Label: "Time since the last ping"},
		}},
}

// Info returns the catalog entry for a type.
func Info(typ string) (TypeInfo, bool) {
	for _, t := range Catalog {
		if t.Type == typ {
			return t, true
		}
	}
	return TypeInfo{}, false
}

// defaultCondition is the condition a monitor of this type uses when alert_when is not set.
func defaultCondition(typ string) string {
	t, _ := Info(typ)
	return t.Default
}
