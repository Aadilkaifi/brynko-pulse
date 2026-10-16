// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"encoding/json"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/duration"
)

// Config is the validated, merged configuration.
type Config struct {
	Version    int    `json:"version"`
	ServerName string `json:"server_name"`
	// ServerAddress is how people reach the server, shown in alerts beside its name.
	ServerAddress string             `json:"server_address"`
	Defaults      Defaults           `json:"defaults"`
	Limits        Limits             `json:"limits"`
	API           API                `json:"api"`
	Channels      map[string]Channel `json:"channels"`
	Monitors      []Monitor          `json:"monitors"`
	Maintenance   []Maintenance      `json:"maintenance"`
	// Secrets is never printed or saved.
	Secrets Secrets `json:"-"`
}

// Defaults apply to every monitor that does not set its own value.
type Defaults struct {
	Severity string    `json:"severity,omitempty"`
	Notify   []string  `json:"notify,omitempty"`
	For      Duration  `json:"for,omitempty"`
	Cooldown Duration  `json:"cooldown,omitempty"`
	Interval Duration  `json:"interval,omitempty"`
	Flapping *Flapping `json:"flapping"`
}

// Flapping configures flapping detection. Nil means the defaults: 5 changes in 30 minutes.
type Flapping struct {
	Changes *int     `json:"changes"` // 0 turns it off
	Window  Duration `json:"window,omitempty"`
}

// FlapLimits returns the effective changes and window.
func (d Defaults) FlapLimits() (int, time.Duration) {
	changes, window := 5, 30*time.Minute
	if d.Flapping != nil {
		if d.Flapping.Changes != nil {
			changes = *d.Flapping.Changes
		}
		if d.Flapping.Window > 0 {
			window = d.Flapping.Window.D()
		}
	}
	return changes, window
}

// Maintenance is a weekly window during which alerts are held back.
type Maintenance struct {
	Name     string   `json:"name,omitempty"`
	Days     []string `json:"days,omitempty"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Timezone string   `json:"timezone,omitempty"`
	Monitors []string `json:"monitors,omitempty"`
}

// Limits bound how much pulse sends and keeps.
type Limits struct {
	MaxAlertsPerMinute int `json:"max_alerts_per_minute"`
	CommandOutputBytes int `json:"command_output_bytes"`
}

// API configures the local API.
type API struct {
	Enabled bool `json:"enabled"`
}

// Channel is one alert destination. Only the fields for its Type are set.
type Channel struct {
	Type        string `json:"type"`
	MinSeverity string `json:"min_severity,omitempty"`

	Host     string   `json:"host,omitempty"`
	Port     int      `json:"port,omitempty"`
	TLS      string   `json:"tls,omitempty"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from,omitempty"`
	To       []string `json:"to,omitempty"`

	PhoneNumberID string `json:"phone_number_id,omitempty"`
	AccessToken   string `json:"access_token,omitempty"`
	Template      string `json:"template,omitempty"`
	Language      string `json:"language,omitempty"`

	BotToken   string `json:"bot_token,omitempty"`
	ChatID     string `json:"chat_id,omitempty"`
	WebhookURL string `json:"webhook_url,omitempty"`

	URL     string   `json:"url,omitempty"`
	Secret  string   `json:"secret,omitempty"`
	Timeout Duration `json:"timeout,omitempty"`

	APIKey   string   `json:"api_key,omitempty"`
	WhatsApp []string `json:"whatsapp,omitempty"`
	Email    []string `json:"email,omitempty"`
}

// Monitor is one thing to watch. Only the fields for its Type are set.
type Monitor struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Name      string   `json:"name,omitempty"`
	Enabled   *bool    `json:"enabled,omitempty"`
	Interval  Duration `json:"interval,omitempty"`
	AlertWhen string   `json:"alert_when,omitempty"`
	ClearWhen string   `json:"clear_when,omitempty"`
	For       Duration `json:"for,omitempty"`
	Severity  string   `json:"severity,omitempty"`
	Notify    []string `json:"notify,omitempty"`
	Cooldown  Duration `json:"cooldown,omitempty"`

	Path            string          `json:"path,omitempty"`
	Service         string          `json:"service,omitempty"`
	Container       string          `json:"container,omitempty"`
	Match           string          `json:"match,omitempty"`
	Host            string          `json:"host,omitempty"`
	Port            int             `json:"port,omitempty"`
	Timeout         Duration        `json:"timeout,omitempty"`
	URL             string          `json:"url,omitempty"`
	Method          string          `json:"method,omitempty"`
	ExpectStatus    json.RawMessage `json:"expect_status,omitempty"`
	BodyContains    string          `json:"body_contains,omitempty"`
	JSONPath        string          `json:"json_path,omitempty"`
	JSONExpect      string          `json:"json_expect,omitempty"`
	FollowRedirects *bool           `json:"follow_redirects,omitempty"`
	File            string          `json:"file,omitempty"`
	Source          *LogSource      `json:"source,omitempty"`
	Pattern         string          `json:"pattern,omitempty"`
	Window          Duration        `json:"window,omitempty"`
	Run             []string        `json:"run,omitempty"`
	Parse           string          `json:"parse,omitempty"`
	ExpectEvery     Duration        `json:"expect_every,omitempty"`
	Grace           Duration        `json:"grace,omitempty"`

	// SourceFile is the config file the monitor came from, for messages.
	SourceFile string `json:"-"`
}

// LogSource names where a log monitor reads from.
type LogSource struct {
	File     string  `json:"file,omitempty"`
	Journal  *string `json:"journal"`
	EventLog string  `json:"eventlog"`
}

// IsEnabled reports whether the monitor should run.
func (m Monitor) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

// DisplayName is the name used in alerts.
func (m Monitor) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// Effective returns the monitor with defaults applied.
func (m Monitor) Effective(d Defaults) Monitor {
	if m.Interval == 0 {
		m.Interval = d.Interval
	}
	if m.Interval == 0 {
		m.Interval = Duration(30 * time.Second)
	}
	if m.For == 0 {
		m.For = d.For
	}
	if m.Cooldown == 0 {
		m.Cooldown = d.Cooldown
	}
	if m.Cooldown == 0 {
		m.Cooldown = Duration(15 * time.Minute)
	}
	if m.Severity == "" {
		m.Severity = d.Severity
	}
	if m.Severity == "" {
		m.Severity = "warning"
	}
	if m.Notify == nil {
		m.Notify = d.Notify
	}
	return m
}

// Duration is a time.Duration written as 30s, 5m, 2h, 1d or combinations like 1h30m.
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// ParseDuration parses the config duration format.
func ParseDuration(s string) (Duration, error) {
	d, err := duration.Parse(s)
	return Duration(d), err
}

// UnmarshalJSON reads the config duration format.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := ParseDuration(s)
	*d = v
	return err
}

// MarshalJSON writes the duration in Go's notation.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(duration.Format(time.Duration(d)))
}
