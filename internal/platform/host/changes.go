// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Change is one thing that changed on the machine, by a person or by a program.
type Change struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`             // sudo, login, package, service, reboot, container or file
	Who    string    `json:"who,omitempty"`    // the account, when the source says
	What   string    `json:"what"`             // one line, already masked
	Detail string    `json:"detail,omitempty"` // more lines when there are any (versions, a diff)
	Source string    `json:"source"`           // where it was read: journal, apt, eventlog, docker, file, boot
}

// The kinds of change.
const (
	ChangeSudo      = "sudo"
	ChangeLogin     = "login"
	ChangePackage   = "package"
	ChangeService   = "service"
	ChangeReboot    = "reboot"
	ChangeContainer = "container"
	ChangeFile      = "file"
)

// ChangeSource reads one operating-system record of changes. Follow sends each change from the
// saved position on until ctx ends, and calls checkpoint with a position to save, so changes made
// while pulse was stopped are read when it starts again. An empty cursor means start fresh.
type ChangeSource interface {
	Name() string
	Follow(ctx context.Context, cursor string, emit func(Change), checkpoint func(cursor string)) error
}

// ── Linux: sudo and sshd lines from the journal, apt's history ──

var (
	sudoRe  = regexp.MustCompile(`^\s*(\S+)\s*:\s*(.*?)\s*COMMAND=(.+)$`)
	sudoKV  = regexp.MustCompile(`(\w+)=([^;]*?)\s*(?:;|$)`)
	loginRe = regexp.MustCompile(`^Accepted (\w+(?:-\w+)*) for (\S+) from (\S+) port \d+`)
)

// ParseSudo reads a sudo log line ("ubuntu : TTY=pts/0 ; PWD=/home/ubuntu ; USER=root ;
// COMMAND=/usr/bin/systemctl restart nginx"). Lines that ran no command (a wrong password, a refusal)
// are not changes.
func ParseSudo(at time.Time, msg string) (Change, bool) {
	m := sudoRe.FindStringSubmatch(msg)
	if m == nil {
		return Change{}, false
	}
	fields := map[string]string{}
	for _, kv := range sudoKV.FindAllStringSubmatch(m[2], -1) {
		fields[kv[1]] = strings.TrimSpace(kv[2])
	}
	if strings.Contains(m[2], "command not allowed") || strings.Contains(m[2], "incorrect password") {
		return Change{}, false
	}
	cmd := strings.TrimSpace(m[3])
	if f := strings.Fields(cmd); len(f) > 0 {
		cmd = strings.TrimSpace(filepath.Base(f[0]) + " " + strings.Join(f[1:], " "))
	}
	detail := []string{}
	if u := fields["USER"]; u != "" && u != "root" {
		detail = append(detail, "as "+u)
	}
	if d := fields["PWD"]; d != "" {
		detail = append(detail, "in "+d)
	}
	return Change{At: at, Kind: ChangeSudo, Who: m[1], What: MaskSecrets(cmd), Detail: strings.Join(detail, ", "),
		Source: "journal"}, true
}

// ParseLogin reads an sshd line ("Accepted publickey for ubuntu from 203.0.113.5 port 52114 ssh2").
func ParseLogin(at time.Time, msg string) (Change, bool) {
	m := loginRe.FindStringSubmatch(msg)
	if m == nil {
		return Change{}, false
	}
	how := map[string]string{"publickey": "a key", "password": "a password", "keyboard-interactive": "a password"}[m[1]]
	if how == "" {
		how = m[1]
	}
	return Change{At: at, Kind: ChangeLogin, Who: m[2], What: "signed in from " + m[3] + " with " + how, Source: "journal"}, true
}

// ParseAptHistory reads the blocks of /var/log/apt/history.log (Start-Date, Commandline,
// Requested-By, Install / Upgrade / Remove / Purge, End-Date), one change per block that changed
// something. The time is the block's start, in the machine's local time.
func ParseAptHistory(text string, loc *time.Location) []Change {
	var out []Change
	for _, block := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		fields := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			if k, v, ok := strings.Cut(line, ": "); ok {
				fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
		at, err := time.ParseInLocation("2006-01-02  15:04:05", fields["Start-Date"], loc)
		if err != nil {
			continue
		}
		var did, detail []string
		for _, verb := range []string{"Install", "Upgrade", "Downgrade", "Remove", "Purge"} {
			if fields[verb] == "" {
				continue
			}
			names := aptPackages(fields[verb])
			did = append(did, fmt.Sprintf("%s %s", aptVerb[verb], countPackages(names)))
			detail = append(detail, aptVerb[verb]+": "+strings.Join(names, ", "))
		}
		if len(did) == 0 {
			continue
		}
		who := ""
		if r := fields["Requested-By"]; r != "" {
			who = strings.Fields(r)[0]
		} else if strings.Contains(fields["Commandline"], "unattended-upgrade") {
			who = "automatic updates"
		}
		out = append(out, Change{At: at, Kind: ChangePackage, Who: who, What: strings.Join(did, ", "),
			Detail: strings.Join(detail, "\n"), Source: "apt"})
	}
	return out
}

var aptVerb = map[string]string{"Install": "installed", "Upgrade": "upgraded", "Downgrade": "downgraded",
	"Remove": "removed", "Purge": "purged"}

// aptPackages turns "libssl3:amd64 (3.0.2-1, 3.0.2-2), openssl:amd64 (3.0.2-1, 3.0.2-2)" into the names.
func aptPackages(list string) []string {
	var names []string
	depth, start := 0, 0
	for i, r := range list + "," {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				item := strings.TrimSpace(list[start:min(i, len(list))])
				if name, _, _ := strings.Cut(item, " "); name != "" {
					name, _, _ = strings.Cut(name, ":")
					names = append(names, name)
				}
				start = i + 1
			}
		}
	}
	return names
}

func countPackages(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	shown := names
	if len(shown) > 3 {
		shown = shown[:3]
	}
	more := ""
	if len(names) > 3 {
		more = fmt.Sprintf(" and %d more", len(names)-3)
	}
	return fmt.Sprintf("%d packages: %s%s", len(names), strings.Join(shown, ", "), more)
}

// ── Windows: events that mean a change ──

var (
	eventProvider = regexp.MustCompile(`provider=(\S+)`)
	eventID       = regexp.MustCompile(`\bid=(\d+)`)
	userInText    = regexp.MustCompile(`\b([\w.-]+\\[\w.$-]+)\b`)
)

// ParseWindowsChange reads one Event Log line as the platform renders it ("provider=X id=N
// level=information <the event's data>") and returns the change it records, if it is one: a service
// installed (7045) or its start type changed (7040), a restart or shutdown someone started (1074),
// software installed or removed (MsiInstaller 11707, 11724) or a Windows update installed (19).
func ParseWindowsChange(at time.Time, line string) (Change, bool) {
	p, id := "", ""
	if m := eventProvider.FindStringSubmatch(line); m != nil {
		p = m[1]
	}
	if m := eventID.FindStringSubmatch(line); m != nil {
		id = m[1]
	}
	data := line
	if i := strings.Index(line, "level="); i >= 0 {
		if j := strings.Index(line[i:], " "); j >= 0 {
			data = strings.TrimSpace(line[i+j:])
		}
	}
	data = oneLine(data, 160)
	c := Change{At: at, Source: "eventlog"}
	switch {
	case strings.HasPrefix(p, "Service") && id == "7045": // the provider is "Service Control Manager"
		c.Kind, c.What = ChangeService, "service installed: "+data
	case strings.HasPrefix(p, "Service") && id == "7040":
		c.Kind, c.What = ChangeService, "service start type changed: "+data
	case id == "1074":
		c.Kind, c.What = ChangeReboot, "restart or shutdown started: "+data
		if m := userInText.FindStringSubmatch(data); m != nil {
			c.Who = m[1]
		}
	case p == "MsiInstaller" && id == "11707":
		c.Kind, c.What = ChangePackage, "installed: "+data
	case p == "MsiInstaller" && id == "11724":
		c.Kind, c.What = ChangePackage, "removed: "+data
	case strings.Contains(p, "WindowsUpdateClient") && id == "19":
		c.Kind, c.What = ChangePackage, "Windows update installed: "+data
	default:
		return Change{}, false
	}
	c.What = MaskSecrets(c.What)
	return c, true
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > limit {
		s = s[:limit-3] + "..."
	}
	return s
}

// ── masking ──

var secretArgs = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(--?(?:password|passwd|pass|pwd|secret|token|api[-_]?key|key)[= ])(\S+)`),
	regexp.MustCompile(`(?i)\b((?:[A-Z0-9_]*(?:PASSWORD|PASSWD|SECRET|TOKEN|API_KEY))=)(\S+)`),
	regexp.MustCompile(`(\s-p)(\S+)`), // mysql -pSecret
	regexp.MustCompile(`(?i)(identified by\s+)('[^']*'|"[^"]*"|\S+)`),
	regexp.MustCompile(`(?i)(://[^:/\s]+:)([^@\s]+)(@)`), // user:secret@host in a URL
}

// MaskSecrets hides what looks like a password, token or key in a command line, so a change never
// carries one. It errs on the side of hiding.
func MaskSecrets(s string) string {
	for _, re := range secretArgs {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			parts := re.FindStringSubmatch(m)
			out := parts[1] + "****"
			if len(parts) > 3 {
				out += parts[3]
			}
			return out
		})
	}
	return s
}
