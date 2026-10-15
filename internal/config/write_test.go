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
