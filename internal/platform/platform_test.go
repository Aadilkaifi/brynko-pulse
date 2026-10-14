// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"strings"
	"testing"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/linux"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/windows"
)

func TestPlatforms(t *testing.T) {
	for _, p := range []Platform{Current(), linux.Platform{}, windows.Platform{}} {
		if p.Name() == "" {
			t.Errorf("%T: empty name", p)
		}
		for _, dir := range []string{p.ConfigDir(), p.DataDir()} {
			if !strings.Contains(dir, "brynko-pulse") {
				t.Errorf("%s: %q does not contain brynko-pulse", p.Name(), dir)
			}
		}
	}
}

func TestWindowsUsesProgramData(t *testing.T) {
	t.Setenv("ProgramData", "D:\\Data")
	if got := (windows.Platform{}).ConfigDir(); !strings.HasPrefix(got, "D:\\Data") {
		t.Fatalf("ConfigDir = %q, want it under ProgramData", got)
	}
}
