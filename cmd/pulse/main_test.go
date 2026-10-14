// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func runCmd(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestUsage(t *testing.T) {
	if code, _, _ := runCmd(); code != exitUsage {
		t.Errorf("no args: exit %d, want %d", code, exitUsage)
	}
	if code, _, stderr := runCmd("nope"); code != exitUsage || !strings.Contains(stderr, "Unknown command") {
		t.Errorf("unknown command: exit %d, stderr %q", code, stderr)
	}
	if code, out, _ := runCmd("help"); code != exitOK || !strings.Contains(out, "validate") {
		t.Errorf("help: exit %d", code)
	}
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := runCmd("version", "--json")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"api_version", "version", "go", "os", "arch", "config_version"} {
		if _, ok := v[key]; !ok {
			t.Errorf("missing key %q in %s", key, out)
		}
	}
}

func TestSchemaIsJSON(t *testing.T) {
	_, out, _ := runCmd("schema")
	if !json.Valid([]byte(out)) {
		t.Fatal("schema output is not valid JSON")
	}
}

func TestValidateJSONContract(t *testing.T) {
	code, out, _ := runCmd("validate", "--json", "--config", filepath.Join("..", "..", "examples"))
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	var v struct {
		APIVersion int    `json:"api_version"`
		Command    string `json:"command"`
		OK         *bool  `json:"ok"`
		Dir        string `json:"config_dir"`
		Errors     []any  `json:"errors"`
		Warnings   []any  `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v.APIVersion != 1 || v.Command != "validate" || v.OK == nil || !*v.OK || v.Errors == nil || v.Warnings == nil {
		t.Fatalf("unexpected contract: %s", out)
	}
}

func TestValidateFailsOnMissingDir(t *testing.T) {
	code, out, _ := runCmd("validate", "--config", t.TempDir())
	if code != exitInvalid || !strings.Contains(out, "pulse.yaml is missing") {
		t.Fatalf("exit %d, out %q", code, out)
	}
}

func TestInit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	code, out, errOut := runCmd("init", "--config", dir, "--server-name", "web-9")
	if code != exitOK || !strings.Contains(out, "pulse validate") {
		t.Fatalf("exit %d, out %q, err %q", code, out, errOut)
	}
	code, _, errOut = runCmd("init", "--config", dir)
	if code != exitInvalid || !strings.Contains(errOut, "already set up") {
		t.Fatalf("second init: exit %d, err %q", code, errOut)
	}
	code, out, _ = runCmd("init", "--json", "--config", dir)
	var v struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(out), &v) != nil || v.OK || v.Error == "" || code != exitInvalid {
		t.Fatalf("json init on existing dir: exit %d, %s", code, out)
	}
}
