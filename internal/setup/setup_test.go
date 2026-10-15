// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDir(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "cfg")
	root := string(filepath.Separator)
	if v := filepath.VolumeName(abs); v != "" {
		root = v + root
	}
	for dir, ok := range map[string]bool{
		abs:              true,
		"":               false,
		"relative/dir":   false,
		root:             false,
		abs + "\nbad":    false,
		abs + "\x7f":     false,
		abs + " with sp": true,
	} {
		if err := CheckDir("test", dir); (err == nil) != ok {
			t.Errorf("CheckDir(%q) = %v, want ok=%v", dir, err, ok)
		}
	}
}

func TestPurge(t *testing.T) {
	base := t.TempDir()
	cfg, data := filepath.Join(base, "cfg"), filepath.Join(base, "data")
	write(t, filepath.Join(cfg, "pulse.yaml"))
	write(t, filepath.Join(data, "state.json"))
	steps, err := Purge(cfg, data)
	if err != nil || len(steps) != 2 {
		t.Fatalf("Purge = %v, %v", steps, err)
	}
	for _, d := range []string{cfg, data} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s still exists", d)
		}
	}
	if steps, err := Purge(cfg, data); err != nil || len(steps) != 0 {
		t.Fatalf("missing folders should be skipped: %v, %v", steps, err)
	}
}

func TestPurgeRefusesForeignFolders(t *testing.T) {
	base := t.TempDir()
	cfg, data := filepath.Join(base, "cfg"), filepath.Join(base, "home")
	write(t, filepath.Join(cfg, "pulse.yaml"))
	write(t, filepath.Join(data, "important.txt"))
	if _, err := Purge(cfg, data); err == nil {
		t.Fatal("a folder without pulse files must not be deleted")
	}
	for _, p := range []string{filepath.Join(data, "important.txt"), filepath.Join(cfg, "pulse.yaml")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was deleted", p)
		}
	}
}

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
}
