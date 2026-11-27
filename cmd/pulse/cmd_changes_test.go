// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// writeChanges stores a timeline the way the recorder does.
func writeChanges(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	dir := filepath.Join(data, "changes")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	list := []host.Change{
		{At: now.Add(-3 * time.Hour), Kind: host.ChangePackage, Who: "ubuntu", What: "installed htop", Source: "apt"},
		{At: now.Add(-2 * time.Hour), Kind: host.ChangeSudo, Who: "ubuntu", What: "systemctl restart nginx", Detail: "in /srv", Source: "journal"},
		{At: now.Add(-time.Hour), Kind: host.ChangeFile, Who: "ubuntu", What: "/etc/nginx/nginx.conf changed", Source: "file",
			Detail: "- a\n+ b\n- c\n+ d\n- e\n+ f\n- g\n+ h"},
		{At: now.Add(-3 * 24 * time.Hour), Kind: host.ChangeLogin, Who: "deploy", What: "signed in from 10.0.0.9 with a key", Source: "journal"},
	}
	var buf []byte
	for _, c := range list {
		b, _ := json.Marshal(c)
		buf = append(append(buf, b...), '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "changes.jsonl"), buf, 0o640); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestChangesJSONContract(t *testing.T) {
	data := writeChanges(t)
	code, out, _ := runCmd("changes", "--json", "--data", data)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	var v struct {
		APIVersion int           `json:"api_version"`
		Command    string        `json:"command"`
		OK         bool          `json:"ok"`
		Since      time.Time     `json:"since"`
		Changes    []host.Change `json:"changes"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v.APIVersion != 1 || v.Command != "changes" || !v.OK || len(v.Changes) != 3 || v.Since.IsZero() {
		t.Fatalf("unexpected: %s", out)
	}
	if v.Changes[0].Kind != host.ChangeFile || v.Changes[2].Kind != host.ChangePackage {
		t.Fatalf("newest first: %+v", v.Changes)
	}
	code, out, _ = runCmd("changes", "--json", "--since", "7d", "--kind", "login,sudo", "--data", data)
	if code != exitOK || strings.Count(out, `"kind"`) != 2 || strings.Contains(out, `"file"`) {
		t.Fatalf("--kind keeps only those: %s", out)
	}
}

func TestChangesText(t *testing.T) {
	code, out, _ := runCmd("changes", "--data", writeChanges(t))
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"3 changes in the last 24h, newest first", "systemctl restart nginx", "in /srv",
		"/etc/nginx/nginx.conf changed", "(2 more lines in --json)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "nginx.conf changed") > strings.Index(out, "installed htop") {
		t.Error("newest first")
	}
	if strings.Contains(out, "—") {
		t.Error("no em dash in user-facing text")
	}
}

func TestChangesBeforeAnyRecorded(t *testing.T) {
	code, out, _ := runCmd("changes", "--data", t.TempDir())
	if code != exitOK || !strings.Contains(out, "No changes in the last 24h") {
		t.Fatalf("exit %d: %s", code, out)
	}
	code, out, _ = runCmd("changes", "--json", "--data", t.TempDir())
	if code != exitOK || !strings.Contains(out, `"changes": []`) || !strings.Contains(out, `"problems": {}`) {
		t.Fatalf("empty, never null: %s", out)
	}
}

func TestChangesSaysWhatIsNotRecorded(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "changes")
	_ = os.MkdirAll(dir, 0o750)
	state := `{"problems":{"journal":"the service cannot read the system journal","docker":"cannot reach Docker"}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o640); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCmd("changes", "--data", data)
	if code != exitOK || !strings.Contains(out, "No changes") || !strings.Contains(out, "Not recorded right now:") ||
		strings.Index(out, "  docker: cannot reach Docker") > strings.Index(out, "  journal: the service cannot") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	code, out, _ = runCmd("changes", "--json", "--data", data)
	if code != exitOK || !strings.Contains(out, `"journal": "the service cannot read the system journal"`) {
		t.Fatalf("%s", out)
	}
}

func TestChangesRefusesBadInput(t *testing.T) {
	for _, args := range [][]string{{"--since", "120d"}, {"--since", "0m"}, {"--kind", "sudo,typo"}} {
		args = append([]string{"changes", "--data", t.TempDir()}, args...)
		if code, _, errOut := runCmd(args...); code != exitInvalid || errOut == "" {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
}
