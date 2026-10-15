// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"strings"
	"testing"
)

func TestParseSecrets(t *testing.T) {
	s, issues := parseSecrets([]byte("\uFEFF# comment\n\nA=1\n B = two words \nC=\"quoted\"\nD='single'\nE=\nF=a=b\n"))
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	want := map[string]string{"A": "1", "B": "two words", "C": "quoted", "D": "single", "E": "", "F": "a=b"}
	for k, v := range want {
		if s[k] != v {
			t.Errorf("%s = %q, want %q", k, s[k], v)
		}
	}
}

func TestParseSecretsErrorsNeverShowValues(t *testing.T) {
	_, issues := parseSecrets([]byte("no-equals-hunter2\n1BAD=hunter2\nOK=x\nOK=hunter2\n"))
	if len(issues) != 3 {
		t.Fatalf("want 3 issues, got %+v", issues)
	}
	for i, line := range []int{1, 2, 4} {
		if issues[i].Line != line {
			t.Errorf("issue %d on line %d, want %d", i, issues[i].Line, line)
		}
		if strings.Contains(issues[i].Message, "hunter2") && !strings.Contains(issues[i].Message, "no-equals") {
			t.Errorf("issue leaks a value: %q", issues[i].Message)
		}
	}
}

func TestSecretsEnvironmentWins(t *testing.T) {
	s := Secrets{"PULSE_T1": "file", "PULSE_T2": "file"}
	t.Setenv("PULSE_T1", "env")
	if got := s.Resolve("${PULSE_T1}"); got != "env" {
		t.Errorf("env should win, got %q", got)
	}
	if got := s.Resolve("${PULSE_T2}"); got != "file" {
		t.Errorf("got %q", got)
	}
	if got := s.Resolve("plain"); got != "plain" {
		t.Errorf("plain value changed: %q", got)
	}
	if got := s.Resolve("${PULSE_UNSET_X}"); got != "" {
		t.Errorf("unset should be empty, got %q", got)
	}
}

func TestSecretsFileSatisfiesValidation(t *testing.T) {
	dir := writeConfig(t, base(nil))
	if res := Validate(dir); !hasWarning(res, "PULSE_TEST_PASSWORD is not set") {
		t.Fatalf("expected a missing-secret warning, got %+v", res.Warnings)
	}
	dir = writeConfig(t, base(map[string]string{SecretsFile: "PULSE_TEST_PASSWORD=s3cret\n"}))
	cfg, res := Load(dir)
	if !res.OK() || hasWarning(res, "PULSE_TEST_PASSWORD") {
		t.Fatalf("secrets.env should satisfy the reference: %+v", res)
	}
	if got := cfg.Secrets.Resolve(cfg.Channels["mail"].Password); got != "s3cret" {
		t.Fatalf("resolved %q", got)
	}
}

func TestBadSecretsFileIsAnError(t *testing.T) {
	dir := writeConfig(t, base(map[string]string{SecretsFile: "not a line\n"}))
	res := Validate(dir)
	if res.OK() || res.Errors[0].File != SecretsFile || res.Errors[0].Line != 1 {
		t.Fatalf("got %+v", res.Errors)
	}
}

func hasWarning(r Result, sub string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w.Message, sub) {
			return true
		}
	}
	return false
}
