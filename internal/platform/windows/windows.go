// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package windows implements the platform for Windows.
package windows

import (
	"os"
	"path/filepath"
)

// Platform is the Windows implementation.
type Platform struct{}

// Name returns "windows".
func (Platform) Name() string { return "windows" }

// ConfigDir is where pulse.yaml, channels.yaml and monitors/ live.
func (Platform) ConfigDir() string { return filepath.Join(programData(), "brynko-pulse") }

// DataDir holds state and alert history.
func (Platform) DataDir() string { return filepath.Join(programData(), "brynko-pulse", "data") }

// RootDisk is the system drive.
func (Platform) RootDisk() string {
	if d := os.Getenv("SystemDrive"); d != "" {
		return d + `\`
	}
	return `C:\`
}

func programData() string {
	if dir := os.Getenv("ProgramData"); dir != "" {
		return dir
	}
	return `C:\ProgramData`
}
