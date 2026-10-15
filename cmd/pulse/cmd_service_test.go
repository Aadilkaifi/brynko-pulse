// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// fakeService records what install and uninstall asked the platform to do.
type fakeService struct {
	platform.Platform
	installed   *host.InstallOptions
	uninstalled *host.UninstallOptions
	err         error
	reloaded    bool
	reloads     int
}

func (f *fakeService) ReloadService(context.Context) (bool, error) {
	f.reloads++
	return f.reloaded, nil
}

func (f *fakeService) Install(_ context.Context, o host.InstallOptions) (host.InstallResult, error) {
	f.installed = &o
	return host.InstallResult{Service: host.ServiceName, Steps: []string{"did it"}, Next: []string{"pulse status"}}, f.err
}

func (f *fakeService) Uninstall(_ context.Context, o host.UninstallOptions) (host.UninstallResult, error) {
	f.uninstalled = &o
	return host.UninstallResult{Steps: []string{"removed"}}, f.err
}

func useFake(t *testing.T, err error) *fakeService {
	f := &fakeService{Platform: platform.Current(), err: err}
	platformFor = func() platform.Platform { return f }
	t.Cleanup(func() { platformFor = platform.Current })
	return f
}

func TestUninstallFlags(t *testing.T) {
	if code, _, _ := runCmd("uninstall", "--yes"); code != exitUsage {
		t.Error("--yes without --purge should be a usage error")
	}
	if code, _, _ := runCmd("uninstall", "extra"); code != exitUsage {
		t.Error("extra arguments should be a usage error")
	}
}

func TestPurgeNeedsConfirmation(t *testing.T) {
	f := useFake(t, nil)
	cfg := filepath.Join(t.TempDir(), "cfg")
	if code, _, errOut := runCmd("init", "--config", cfg); code != exitOK {
		t.Fatal(errOut)
	}

	code, out, _ := runCmd("uninstall", "--purge", "--json", "--config", cfg, "--data", t.TempDir())
	var v struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(out), &v) != nil || v.OK || !strings.Contains(v.Error, "--yes") || code != exitInvalid {
		t.Fatalf("--json purge without --yes: exit %d, %s", code, out)
	}

	stdin = strings.NewReader("no\n")
	defer func() { stdin = os.Stdin }()
	code, out, errOut := runCmd("uninstall", "--purge", "--config", cfg, "--data", t.TempDir())
	if code != exitInvalid || !strings.Contains(out, "Type delete") || !strings.Contains(errOut, "not confirmed") {
		t.Fatalf("exit %d, out %q, err %q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(cfg, "pulse.yaml")); err != nil || f.uninstalled != nil {
		t.Fatal("an unconfirmed purge must not change anything")
	}
}

func TestInstallCreatesConfigFirst(t *testing.T) {
	f := useFake(t, nil)
	cfg, data := filepath.Join(t.TempDir(), "cfg"), t.TempDir()
	code, out, errOut := runCmd("install", "--config", cfg, "--data", data)
	if code != exitOK {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if !strings.Contains(out, "Created the starter configuration") || !strings.Contains(out, "pulse status") {
		t.Errorf("output: %q", out)
	}
	if f.installed == nil || f.installed.ConfigDir != cfg || f.installed.DataDir != data || f.installed.Source == "" {
		t.Fatalf("install options: %+v", f.installed)
	}
}

func TestInstallStopsOnBadConfig(t *testing.T) {
	f := useFake(t, nil)
	cfg := filepath.Join(t.TempDir(), "cfg")
	if code, _, errOut := runCmd("init", "--config", cfg); code != exitOK {
		t.Fatal(errOut)
	}
	if err := os.WriteFile(filepath.Join(cfg, "pulse.yaml"), []byte("version: 1\nnope: true\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd("install", "--config", cfg, "--data", t.TempDir()); code != exitInvalid || !strings.Contains(errOut, "errors") {
		t.Fatalf("exit %d, %q", code, errOut)
	}
	if f.installed != nil {
		t.Fatal("a bad configuration must stop install before the system is changed")
	}
}

func TestInstallReportsPlatformError(t *testing.T) {
	useFake(t, fmt.Errorf("install: %w", host.ErrNotAdmin))
	code, out, _ := runCmd("install", "--json", "--config", filepath.Join(t.TempDir(), "cfg"), "--data", t.TempDir())
	var v struct {
		OK      bool     `json:"ok"`
		Error   string   `json:"error"`
		Created bool     `json:"config_created"`
		Steps   []string `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.OK || !v.Created || !strings.Contains(v.Error, "Administrator") || code != exitInvalid {
		t.Fatalf("exit %d, %s", code, out)
	}
}

func TestPurgeDeletesAfterConfirmation(t *testing.T) {
	f := useFake(t, nil)
	cfg, data := filepath.Join(t.TempDir(), "cfg"), filepath.Join(t.TempDir(), "data")
	if code, _, errOut := runCmd("init", "--config", cfg); code != exitOK {
		t.Fatal(errOut)
	}
	if code, _, _ := runCmd("ping", "--data", data, "job"); code != exitOK {
		t.Fatal("ping")
	}
	stdin = strings.NewReader("delete\n")
	defer func() { stdin = os.Stdin }()
	if code, out, errOut := runCmd("uninstall", "--purge", "--config", cfg, "--data", data); code != exitOK {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if f.uninstalled == nil || !f.uninstalled.Purge {
		t.Fatal("platform uninstall not asked to purge")
	}
	for _, d := range []string{cfg, data} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s still exists", d)
		}
	}
}

func TestInstallRefusesRelativeRoot(t *testing.T) {
	root := string(filepath.Separator)
	if code, _, errOut := runCmd("install", "--config", root, "--data", t.TempDir()); code != exitInvalid || !strings.Contains(errOut, "root of a disk") {
		t.Fatalf("exit %d, %q", code, errOut)
	}
}
