// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigShowSetDelete(t *testing.T) {
	useFake(t, nil)
	dir := initDir(t)
	code, out, _ := runCmd("config", "show", "--json", "--config", dir)
	var v struct {
		OK       bool             `json:"ok"`
		Valid    bool             `json:"valid"`
		Files    []string         `json:"files"`
		Secrets  []string         `json:"secrets"`
		Monitors []map[string]any `json:"monitors"`
		Channels map[string]any   `json:"channels"`
	}
	if code != exitOK || json.Unmarshal([]byte(out), &v) != nil || !v.Valid || len(v.Monitors) != 3 || v.Monitors[0]["file"] == "" {
		t.Fatalf("show: %d %s", code, out)
	}
	for _, f := range v.Files {
		if strings.Contains(f, "secrets") {
			t.Fatal("secrets.env must never be listed")
		}
	}

	stdin = strings.NewReader("monitors:\n  - id: web\n    type: port\n    port: 80\n")
	defer func() { stdin = os.Stdin }()
	if code, out, errOut := runCmd("config", "set", "monitors/web.yaml", "--config", dir); code != exitOK {
		t.Fatalf("set: %s %s", out, errOut)
	}
	stdin = strings.NewReader("monitors:\n  - id: web\n    type: nope\n")
	if code, _, _ := runCmd("config", "set", "monitors/web.yaml", "--config", dir); code != exitInvalid {
		t.Fatal("an invalid file must be refused")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "monitors", "web.yaml")); !strings.Contains(string(data), "port: 80") {
		t.Fatal("a refused change must leave the file as it was")
	}
	if code, out, _ := runCmd("config", "get", "monitors/web.yaml", "--config", dir); code != exitOK || !strings.Contains(out, "port: 80") {
		t.Fatalf("get: %s", out)
	}
	if code, _, _ := runCmd("config", "get", "secrets.env", "--config", dir); code != exitInvalid {
		t.Fatal("secrets.env must never be served")
	}
	if code, _, _ := runCmd("config", "delete", "monitors/web.yaml", "--config", dir); code != exitOK {
		t.Fatal("delete")
	}
	if code, _, _ := runCmd("config", "delete", "pulse.yaml", "--config", dir); code != exitInvalid {
		t.Fatal("pulse.yaml must not be deletable")
	}
}

func TestSecretSet(t *testing.T) {
	useFake(t, nil)
	dir := initDir(t)
	stdin = strings.NewReader("s3cret\n")
	defer func() { stdin = os.Stdin }()
	code, out, _ := runCmd("secret", "set", "PULSE_SMTP_PASSWORD", "--json", "--config", dir)
	if code != exitOK || strings.Contains(out, "s3cret") {
		t.Fatalf("set: %d %s", code, out)
	}
	_, out, _ = runCmd("config", "show", "--json", "--config", dir)
	if !strings.Contains(out, `"PULSE_SMTP_PASSWORD"`) || strings.Contains(out, "s3cret") {
		t.Fatalf("show must list the name and never the value: %s", out)
	}
	stdin = strings.NewReader("")
	if code, _, _ := runCmd("secret", "set", "X", "--config", dir); code != exitInvalid {
		t.Fatal("an empty value must be refused")
	}
	if code, _, _ := runCmd("secret", "delete", "PULSE_SMTP_PASSWORD", "--config", dir); code != exitOK {
		t.Fatal("delete")
	}
}

func TestConfigSetDryRunChecksAndTestsWithoutSaving(t *testing.T) {
	useFake(t, nil)
	dir := initDir(t)
	defer func() { stdin = os.Stdin }()
	yaml := "monitors:\n  - id: tmp-disk\n    type: disk\n    path: " + filepath.ToSlash(dir) + "\n    alert_when: used_percent > 1000\n"
	stdin = strings.NewReader(yaml)
	code, out, errOut := runCmd("config", "set", "monitors/tmp-disk.yaml", "--dry-run", "--json", "--config", dir, "--data", t.TempDir())
	var v struct {
		OK     bool `json:"ok"`
		Saved  bool `json:"saved"`
		DryRun bool `json:"dry_run"`
		Tests  []struct {
			Monitor    string         `json:"monitor"`
			Values     map[string]any `json:"values"`
			WouldAlert bool           `json:"would_alert"`
		} `json:"tests"`
	}
	if code != exitOK || json.Unmarshal([]byte(out), &v) != nil || !v.OK || v.Saved || !v.DryRun {
		t.Fatalf("dry run: %d %s %s", code, out, errOut)
	}
	if len(v.Tests) != 1 || v.Tests[0].Monitor != "tmp-disk" || v.Tests[0].WouldAlert || v.Tests[0].Values["used_percent"] == nil {
		t.Fatalf("the monitor in the file must be tested once: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "monitors", "tmp-disk.yaml")); err == nil {
		t.Fatal("a dry run must not save")
	}

	stdin = strings.NewReader("monitors:\n  - id: bad\n    type: nope\n")
	code, out, _ = runCmd("config", "set", "monitors/bad.yaml", "--dry-run", "--json", "--config", dir)
	if code != exitInvalid || !strings.Contains(out, `"errors"`) || strings.Contains(out, `"errors": []`) {
		t.Fatalf("an invalid file must report its errors: %s", out)
	}
}

func TestFlagsMayFollowArguments(t *testing.T) {
	useFake(t, nil)
	dir := initDir(t)
	code, out, _ := runCmd("test", "monitor", "nope", "--json", "--config", dir)
	if code != exitInvalid || !strings.Contains(out, `"ok": false`) {
		t.Fatalf("flags after the arguments must still apply: %d %s", code, out)
	}
}
