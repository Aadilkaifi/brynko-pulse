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

func initDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cfg")
	if code, _, errOut := runCmd("init", "--config", dir); code != exitOK {
		t.Fatal(errOut)
	}
	return dir
}

func TestAddSavesAndReloads(t *testing.T) {
	f := useFake(t, nil)
	f.reloaded = true
	dir := initDir(t)
	code, out, errOut := runCmd("add", "disk", "/data", "--above", "70", "--config", dir)
	if code != exitOK || !strings.Contains(out, "picked it up") {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	data, err := os.ReadFile(filepath.Join(dir, "monitors", "disk-data.yaml"))
	if err != nil || !strings.Contains(string(data), "used_percent > 70") {
		t.Fatalf("file: %s %v", data, err)
	}
	if f.reloads != 1 {
		t.Errorf("reloads = %d", f.reloads)
	}
	if code, _, errOut := runCmd("add", "disk", "/data", "--config", dir); code != exitInvalid || !strings.Contains(errOut, "--id") {
		t.Fatalf("second add: %d %q", code, errOut)
	}
}

func TestAddRefusesInvalidAndDryRun(t *testing.T) {
	f := useFake(t, nil)
	dir := initDir(t)
	code, out, _ := runCmd("add", "--json", "service", "nginx", "--notify", "missing", "--config", dir)
	var v struct {
		OK     bool  `json:"ok"`
		Saved  bool  `json:"saved"`
		Issues []any `json:"issues"`
	}
	if json.Unmarshal([]byte(out), &v) != nil || v.OK || v.Saved || len(v.Issues) == 0 || code != exitInvalid {
		t.Fatalf("exit %d, %s", code, out)
	}
	if code, out, _ := runCmd("add", "port", "5432", "--dry-run", "--config", dir); code != exitOK || !strings.Contains(out, "Nothing was saved") {
		t.Fatalf("dry run: %d %s", code, out)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "monitors")); len(entries) != 2 || f.reloads != 0 {
		t.Fatalf("nothing should be written or reloaded: %d files, %d reloads", len(entries), f.reloads)
	}
}

func TestAddCommandAfterDashes(t *testing.T) {
	useFake(t, nil)
	dir := initDir(t)
	code, _, errOut := runCmd("add", "command", "--id", "q", "--config", dir, "--", "/bin/check", "--max", "5")
	if code != exitOK {
		t.Fatal(errOut)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "monitors", "q.yaml"))
	if !strings.Contains(string(data), "- --max") {
		t.Fatalf("arguments after -- must reach the command:\n%s", data)
	}
}

func TestSilenceCommands(t *testing.T) {
	useFake(t, nil)
	dir, data := initDir(t), t.TempDir()
	run := func(args ...string) (int, string, string) {
		return runCmd(append([]string{"silence", "--config", dir, "--data", data}, args...)...)
	}
	if code, out, errOut := run("memory", "1h", "--reason", "deploy"); code != exitOK || !strings.Contains(out, "silenced until") {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if code, _, errOut := run("nope", "1h"); code != exitInvalid || !strings.Contains(errOut, "no monitor") {
		t.Fatalf("unknown id: %d %q", code, errOut)
	}
	for _, bad := range [][]string{{"memory", "soon"}, {"memory", "40d"}, {"memory", "0s"}} {
		if code, _, _ := run(bad...); code != exitInvalid {
			t.Errorf("%v should fail", bad)
		}
	}
	code, out, _ := run("list", "--json")
	if code != exitOK || !strings.Contains(out, `"reason": "deploy"`) {
		t.Fatalf("list: %s", out)
	}
	if code, out, _ := run("clear", "all"); code != exitOK || !strings.Contains(out, "Ended 1") {
		t.Fatalf("clear: %s", out)
	}
	if code, _, _ := run(); code != exitUsage {
		t.Error("no arguments should print usage")
	}
}

func TestReorderFlags(t *testing.T) {
	got := strings.Join(reorderFlags([]string{"web", "1h", "--reason", "x y", "--json", "--", "--raw"}), "|")
	if got != "--reason|x y|--json|--|web|1h|--raw" {
		t.Fatalf("got %s", got)
	}
}
