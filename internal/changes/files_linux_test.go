// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package changes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// closed makes a folder the test user can see but not enter, as a 0750 home folder is to the
// service account. Root enters every folder, so the tests need a normal user.
func closed(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root can enter every folder")
	}
	if err := os.Chmod(dir, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

func TestAComposeFileBehindAClosedFolderIsReported(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	compose := filepath.Join(home, "apps", "shop", "compose.yml")
	_ = os.MkdirAll(filepath.Dir(compose), 0o755)
	_ = os.WriteFile(compose, []byte("services: {}\n"), 0o644)
	closed(t, home)

	r, dir := newRec(t, Options{})
	r.compose[compose] = true
	r.ScanFiles(t0)
	r.save()
	got := Problems(dir)["file"]
	if !strings.Contains(got, "1 compose file(s) cannot be read") || !strings.Contains(got, compose) ||
		!strings.Contains(got, "The service cannot enter "+home) || !strings.Contains(got, "setfacl -m u:brynko-pulse:x "+home) {
		t.Fatalf("%q", got)
	}

	_ = os.Chmod(home, 0o755)
	r.ScanFiles(t0.Add(time.Minute))
	r.save()
	if got := Problems(dir); len(got) != 0 {
		t.Fatalf("readable again: %v", got)
	}
}

func TestACommonFileThatCannotBeReadIsSkippedQuietly(t *testing.T) {
	f := filepath.Join(t.TempDir(), "redis.conf")
	_ = os.WriteFile(f, []byte("port 6379\n"), 0o644)
	closed(t, filepath.Dir(f))
	r, dir := newRec(t, Options{Files: func() []string { return []string{f} }})
	r.ScanFiles(t0)
	r.save()
	if got := Problems(dir); len(got) != 0 {
		t.Fatalf("not a problem: %v", got)
	}
}

func TestAFileThatBecomesUnreadableIsKept(t *testing.T) {
	etc := filepath.Join(t.TempDir(), "etc")
	_ = os.Mkdir(etc, 0o755)
	f := filepath.Join(etc, "app.conf")
	_ = os.WriteFile(f, []byte("a\n"), 0o644)
	r, dir := newRec(t, Options{Files: func() []string { return []string{f} }})
	r.ScanFiles(t0)
	closed(t, etc)
	r.ScanFiles(t0.Add(time.Minute))
	_ = os.Chmod(etc, 0o755)
	_ = os.WriteFile(f, []byte("b\n"), 0o644)
	r.ScanFiles(t0.Add(2 * time.Minute))
	got := all(t, dir)
	if len(got) != 1 || got[0].What != f+" changed" {
		t.Fatalf("not removed, and its next change is still seen: %+v", got)
	}
}
