// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const newMonitor = "monitors:\n  - id: web\n    type: port\n    port: 80\n"

func TestApplyWritesValidChange(t *testing.T) {
	dir := writeConfig(t, base(nil))
	res, err := Apply(dir, []Change{{Path: "monitors/web.yaml", Data: []byte(newMonitor), CreateOnly: true}})
	if err != nil || !res.OK() {
		t.Fatalf("Apply = %+v, %v", res, err)
	}
	cfg, r := Load(dir)
	if !r.OK() || len(cfg.Monitors) != 2 {
		t.Fatalf("after apply: %+v", r)
	}
	if _, err := Apply(dir, []Change{{Path: "monitors/web.yaml", Data: []byte(newMonitor), CreateOnly: true}}); err == nil {
		t.Fatal("CreateOnly must not replace an existing file")
	}
}

func TestApplyRefusesInvalidChange(t *testing.T) {
	dir := writeConfig(t, base(nil))
	dup := "monitors:\n  - id: disk\n    type: port\n    port: 80\n" // id already used
	res, err := Apply(dir, []Change{{Path: "monitors/dup.yaml", Data: []byte(dup)}})
	if !errors.Is(err, ErrInvalid) || res.OK() {
		t.Fatalf("want ErrInvalid, got %v %+v", err, res)
	}
	if _, err := os.Stat(filepath.Join(dir, "monitors", "dup.yaml")); !os.IsNotExist(err) {
		t.Fatal("an invalid change must not be written")
	}
	if e := res.Errors[0]; e.File != "monitors/dup.yaml" && !strings.Contains(e.Message, "monitors/dup.yaml") {
		t.Errorf("the issue should name the proposed file: %+v", e)
	}
}

func TestApplyPaths(t *testing.T) {
	dir := writeConfig(t, base(nil))
	for _, p := range []string{"../x.yaml", "/etc/x.yaml", "monitors/../pulse.yaml", "secrets.env",
		"monitors/a/b.yaml", "monitors/x.txt", `monitors\x.yaml`, "other.yaml"} {
		if _, err := Apply(dir, []Change{{Path: p, Data: []byte(newMonitor)}}); err == nil {
			t.Errorf("path %q should be refused", p)
		}
	}
}

func TestReservedMonitorID(t *testing.T) {
	dir := writeConfig(t, base(nil))
	_, err := Apply(dir, []Change{{Path: "monitors/all.yaml", Data: []byte("monitors:\n  - id: all\n    type: memory\n    alert_when: used_percent > 90\n")}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("id all must be refused, got %v", err)
	}
}

func TestMaintenanceValidation(t *testing.T) {
	good := validPulse + "maintenance:\n  - name: patch\n    days: [sun]\n    from: \"02:00\"\n    to: \"03:00\"\n    timezone: Asia/Kolkata\n    monitors: [disk]\n"
	if res := Validate(writeConfig(t, base(map[string]string{PulseFile: good}))); !res.OK() {
		t.Fatalf("valid window refused: %+v", res.Errors)
	}
	for _, bad := range []string{
		"maintenance:\n  - name: p\n    days: [sun]\n    from: \"02:00\"\n    to: \"02:00\"\n",
		"maintenance:\n  - name: p\n    days: [sun]\n    from: \"02:00\"\n    to: \"03:00\"\n    timezone: Mars/Base\n",
		"maintenance:\n  - name: p\n    days: [sun]\n    from: \"02:00\"\n    to: \"03:00\"\n    monitors: [nope]\n",
		"maintenance:\n  - name: p\n    days: [sunday]\n    from: \"02:00\"\n    to: \"03:00\"\n",
		"maintenance:\n  - name: p\n    days: [sun]\n    from: \"2:00\"\n    to: \"03:00\"\n",
		"maintenance:\n  - {name: p, days: [sun], from: \"02:00\", to: \"03:00\"}\n  - {name: p, days: [mon], from: \"02:00\", to: \"03:00\"}\n",
	} {
		if res := Validate(writeConfig(t, base(map[string]string{PulseFile: validPulse + bad}))); res.OK() {
			t.Errorf("should be refused:\n%s", bad)
		}
	}
}

func TestApplyDelete(t *testing.T) {
	dir := writeConfig(t, base(map[string]string{"monitors/web.yaml": newMonitor}))
	if _, err := Apply(dir, []Change{{Path: "monitors/web.yaml", Delete: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "monitors", "web.yaml")); !os.IsNotExist(err) {
		t.Fatal("not deleted")
	}
	for _, p := range []string{PulseFile, ChannelsFile, "monitors/missing.yaml"} {
		if _, err := Apply(dir, []Change{{Path: p, Delete: true}}); err == nil {
			t.Errorf("deleting %s must be refused", p)
		}
	}
	// Deleting a monitor other files depend on is refused like any invalid change.
	dir = writeConfig(t, base(map[string]string{PulseFile: validPulse + "maintenance:\n  - {name: p, days: [sun], from: \"02:00\", to: \"03:00\", monitors: [disk]}\n"}))
	if _, err := Apply(dir, []Change{{Path: "monitors/system.yaml", Delete: true}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestSetSecret(t *testing.T) {
	dir := writeConfig(t, base(map[string]string{SecretsFile: "# keep me\nA=1\n"}))
	if err := SetSecret(dir, "PULSE_TEST_PASSWORD", ` p"a ss `); err != nil {
		t.Fatal(err)
	}
	if err := SetSecret(dir, "A", "2"); err != nil {
		t.Fatal(err)
	}
	s, issues := readSecrets(dirSource(dir))
	if len(issues) != 0 || s["A"] != "2" || s["PULSE_TEST_PASSWORD"] != ` p"a ss ` {
		t.Fatalf("got %v %v", s, issues)
	}
	data, _ := os.ReadFile(SecretsPath(dir))
	if !strings.HasPrefix(string(data), "# keep me\n") {
		t.Fatalf("comments must be kept:\n%s", data)
	}
	if err := SetSecret(dir, "A", ""); err != nil {
		t.Fatal(err)
	}
	if names, _ := SecretNames(dir); len(names) != 1 {
		t.Fatalf("names %v", names)
	}
	for _, bad := range [][2]string{{"1X", "v"}, {"X", "a\nb"}, {"MISSING", ""}} {
		if SetSecret(dir, bad[0], bad[1]) == nil {
			t.Errorf("%v should fail", bad)
		}
	}
}

func TestManySilentMonitorsGiveOneWarning(t *testing.T) {
	mons := "monitors:\n"
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		mons += "  - id: " + id + "\n    type: memory\n    alert_when: used_percent > 90\n"
	}
	res := Validate(writeConfig(t, base(map[string]string{PulseFile: "version: 1\nserver_name: x\n", "monitors/system.yaml": mons})))
	n := 0
	for _, w := range res.Warnings {
		if strings.Contains(w.Message, "no channels") {
			n++
			if !strings.Contains(w.Message, "5 monitors") || !strings.Contains(w.Message, "notify: [mail]") {
				t.Errorf("unhelpful warning: %s", w.Message)
			}
		}
	}
	if n != 1 {
		t.Fatalf("want one combined warning, got %d: %+v", n, res.Warnings)
	}
}
