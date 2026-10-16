// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validPulse = "version: 1\nserver_name: web-1\ndefaults:\n  notify: [mail]\n"
const validChannels = `channels:
  mail:
    type: smtp
    host: smtp.example.com
    from: a@example.com
    to: [b@example.com]
    password: ${PULSE_TEST_PASSWORD}
`
const validMonitors = `monitors:
  - id: disk
    type: disk
    path: /
    alert_when: used_percent > 85
`

func writeConfig(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func base(overrides map[string]string) map[string]string {
	files := map[string]string{
		PulseFile:              validPulse,
		ChannelsFile:           validChannels,
		"monitors/system.yaml": validMonitors,
	}
	for k, v := range overrides {
		if v == "" {
			delete(files, k)
		} else {
			files[k] = v
		}
	}
	return files
}

func TestValidate(t *testing.T) {
	t.Setenv("PULSE_TEST_PASSWORD", "x")

	tests := []struct {
		name     string
		files    map[string]string
		wantErr  string // substring of an error message; empty means valid
		wantFile string
		wantLine int
		wantWarn string
		wantNot  string // no error may contain this
	}{
		{name: "valid", files: base(nil)},
		{name: "missing pulse.yaml", files: base(map[string]string{PulseFile: ""}), wantErr: "pulse.yaml is missing", wantFile: PulseFile},
		{name: "unknown field", files: base(map[string]string{PulseFile: validPulse + "sever_name: x\n"}), wantErr: "unknown field: sever_name", wantLine: 5},
		{name: "valid fields not reported when branch fails", files: base(map[string]string{ChannelsFile: strings.Replace(validChannels, "${PULSE_TEST_PASSWORD}", "hunter2", 1)}), wantErr: "secrets.env", wantNot: "unknown field"},
		{name: "literal secret rejected", files: base(map[string]string{ChannelsFile: strings.Replace(validChannels, "${PULSE_TEST_PASSWORD}", "hunter2", 1)}), wantErr: "secrets.env", wantFile: ChannelsFile, wantLine: 7},
		{name: "bad condition", files: base(map[string]string{"monitors/system.yaml": strings.Replace(validMonitors, "used_percent > 85", "used percent is high", 1)}), wantErr: "One comparison", wantLine: 5},
		{name: "disk needs path", files: base(map[string]string{"monitors/system.yaml": strings.Replace(validMonitors, "    path: /\n", "", 1)}), wantErr: "missing required field: path"},
		{name: "wrong monitor type", files: base(map[string]string{"monitors/system.yaml": strings.Replace(validMonitors, "type: disk", "type: dsk", 1)}), wantErr: "must be one of"},
		{name: "command must be a list", files: base(map[string]string{"monitors/x.yaml": "monitors:\n  - id: q\n    type: command\n    run: \"echo hi\"\n    alert_when: exit_code != 0\n"}), wantErr: "want array"},
		{name: "field from another type", files: base(map[string]string{"monitors/x.yaml": "monitors:\n  - id: m\n    type: memory\n    path: /\n    alert_when: used_percent > 90\n"}), wantErr: "path"},
		{name: "brynko needs a destination", files: base(map[string]string{ChannelsFile: validChannels + "  relay:\n    type: brynko\n    api_key: ${PULSE_TEST_PASSWORD}\n"}), wantErr: "missing required field"},
		{name: "duplicate monitor id", files: base(map[string]string{"monitors/other.yaml": validMonitors}), wantErr: `monitor id "disk" is already used`},
		{name: "unknown channel", files: base(map[string]string{"monitors/x.yaml": "monitors:\n  - id: m\n    type: memory\n    alert_when: used_percent > 90\n    notify: [pager]\n"}), wantErr: `channel "pager" is not defined`},
		{name: "unknown default channel", files: base(map[string]string{PulseFile: "version: 1\nserver_name: web-1\ndefaults:\n  notify: [pager]\n"}), wantErr: `channel "pager" is not defined`, wantFile: PulseFile},
		{name: "aliases rejected", files: base(map[string]string{PulseFile: "version: 1\nserver_name: &n web-1\nx: *n\n"}), wantErr: "anchors and aliases"},
		{name: "duplicate key", files: base(map[string]string{PulseFile: "version: 1\nversion: 1\nserver_name: a\n"}), wantErr: `"version" is defined twice`, wantLine: 2},
		{name: "empty file", files: base(map[string]string{ChannelsFile: "   \n"}), wantErr: "file is empty"},
		{name: "two documents", files: base(map[string]string{PulseFile: validPulse + "---\nversion: 1\n"}), wantErr: "only one YAML document"},
		{name: "yaml syntax error", files: base(map[string]string{PulseFile: "version: 1\nserver_name: [a\n"}), wantErr: "did not find expected"},
		{name: "wrong config version", files: base(map[string]string{PulseFile: strings.Replace(validPulse, "version: 1", "version: 2", 1)}), wantErr: "value must be 1"},
		{name: "no monitors warns", files: base(map[string]string{"monitors/system.yaml": ""}), wantWarn: "no monitors are configured"},
		{name: "unset env warns", files: base(map[string]string{ChannelsFile: strings.Replace(validChannels, "PULSE_TEST_PASSWORD", "PULSE_NOT_SET_ANYWHERE", 1)}), wantWarn: "PULSE_NOT_SET_ANYWHERE is not set"},
		{name: "json_expect valid", files: base(map[string]string{"monitors/x.yaml": "monitors:\n  - id: api\n    type: http\n    url: https://api.example.com/health\n    json_path: data.status\n    json_expect: == ok\n"})},
		{name: "json_expect needs json_path", files: base(map[string]string{"monitors/x.yaml": "monitors:\n  - id: api\n    type: http\n    url: https://a.example.com\n    json_expect: == ok\n"}), wantErr: "set json_path too"},
		{name: "json_expect text with >", files: base(map[string]string{"monitors/x.yaml": "monitors:\n  - id: api\n    type: http\n    url: https://api.example.com/health\n    json_path: data.status\n    json_expect: \"> ok\"\n"}), wantErr: "only be compared with == or !="},
		{name: "json_expect without comparison", files: base(map[string]string{"monitors/x.yaml": "monitors:\n  - id: api\n    type: http\n    url: https://api.example.com/health\n    json_path: data.status\n    json_expect: ok\n"}), wantErr: "as a comparison"},
		{name: "no channels warns", files: base(map[string]string{PulseFile: "version: 1\nserver_name: web-1\n"}), wantWarn: "alerts go nowhere"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := Validate(writeConfig(t, tc.files))
			if tc.wantErr == "" {
				if !res.OK() {
					t.Fatalf("expected valid, got errors: %+v", res.Errors)
				}
			} else {
				found := false
				for _, is := range res.Errors {
					if strings.Contains(is.Message, tc.wantErr) {
						found = true
						if tc.wantFile != "" && is.File != tc.wantFile {
							t.Errorf("file = %q, want %q", is.File, tc.wantFile)
						}
						if tc.wantLine != 0 && is.Line != tc.wantLine {
							t.Errorf("line = %d, want %d", is.Line, tc.wantLine)
						}
					}
				}
				if !found {
					t.Fatalf("no error containing %q; got %+v", tc.wantErr, res.Errors)
				}
			}
			for _, is := range res.Errors {
				if tc.wantNot != "" && strings.Contains(is.Message, tc.wantNot) {
					t.Errorf("unexpected error: %+v", is)
				}
			}
			if tc.wantWarn != "" {
				found := false
				for _, is := range res.Warnings {
					if strings.Contains(is.Message, tc.wantWarn) {
						found = true
					}
				}
				if !found {
					t.Fatalf("no warning containing %q; got %+v", tc.wantWarn, res.Warnings)
				}
			}
		})
	}
}

// The examples are the starter config: valid, and the only warnings are the
// ones that tell a new user to add a channel.
func TestExamplesAreValid(t *testing.T) {
	res := Validate(filepath.Join("..", "..", "examples"))
	if !res.OK() {
		t.Fatalf("examples/ has errors: %+v", res.Errors)
	}
	for _, w := range res.Warnings {
		if !strings.Contains(w.Message, "alerts go nowhere") {
			t.Fatalf("unexpected warning: %+v", w)
		}
	}
}

func TestErrorsAreNeverNil(t *testing.T) {
	res := Validate(writeConfig(t, base(nil)))
	if res.Errors == nil || res.Warnings == nil {
		t.Fatal("Errors and Warnings must be empty slices, not nil, so --json prints []")
	}
}
