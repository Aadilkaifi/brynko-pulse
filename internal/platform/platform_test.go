// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCurrent(t *testing.T) {
	p := Current()
	if p.Name() == "" {
		t.Fatal("empty name")
	}
	for _, dir := range []string{p.ConfigDir(), p.DataDir()} {
		if !strings.Contains(dir, "brynko-pulse") {
			t.Errorf("%q does not contain brynko-pulse", dir)
		}
	}
	if p.RootDisk() == "" {
		t.Error("empty root disk")
	}
}

func TestSystemMetrics(t *testing.T) {
	p := Current()
	if p.Name() == "unsupported" {
		t.Skip("no metrics on this OS")
	}
	d, err := p.DiskUsage(p.RootDisk())
	if err != nil || d.TotalBytes == 0 || d.FreeBytes > d.TotalBytes {
		t.Fatalf("DiskUsage = %+v, %v", d, err)
	}
	m, err := p.Memory()
	if err != nil || m.TotalBytes == 0 || m.AvailableBytes > m.TotalBytes {
		t.Fatalf("Memory = %+v, %v", m, err)
	}
	c, err := p.CPUTimes()
	if err != nil || c.Total == 0 || c.Idle > c.Total {
		t.Fatalf("CPUTimes = %+v, %v", c, err)
	}
	if _, err := p.ProcessCount("no-such-process-xyz"); err != nil {
		t.Fatalf("ProcessCount: %v", err)
	}
	// The machine started before this test did, and not more than ten years ago.
	if up, err := p.Uptime(); err != nil || up <= 0 || up > 10*365*24*time.Hour {
		t.Fatalf("Uptime = %v, %v", up, err)
	}
}

func TestConfigSecure(t *testing.T) {
	p := Current()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pulse.yaml"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := p.SecureConfig(dir); err != nil {
		t.Fatalf("SecureConfig: %v", err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckConfigSecure(dir); err != nil {
		t.Fatalf("a freshly secured folder should pass: %v", err)
	}
	if p.Name() == "linux" {
		if err := os.Chmod(filepath.Join(dir, "pulse.yaml"), 0o666); err != nil {
			t.Fatal(err)
		}
		if err := p.CheckConfigSecure(dir); err == nil {
			t.Fatal("a world-writable file must be refused")
		}
	}
}
