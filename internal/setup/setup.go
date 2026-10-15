// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package setup holds the parts of install and uninstall that are the same on
// every operating system: path checks and removing pulse's own folders.
package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckDir rejects folder paths that are unsafe to hand to a service manager
// or to delete: relative paths, the filesystem root, and control characters.
func CheckDir(label, dir string) error {
	if dir == "" {
		return fmt.Errorf("the %s folder is empty", label)
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("the %s folder must be a full path, not %q", label, dir)
	}
	if strings.ContainsFunc(dir, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("the %s folder %q contains control characters", label, dir)
	}
	if isRoot(dir) {
		return fmt.Errorf("the %s folder cannot be the root of a disk", label)
	}
	return nil
}

func isRoot(dir string) bool {
	clean := filepath.Clean(dir)
	return clean == filepath.VolumeName(clean)+string(filepath.Separator)
}

// Files that prove a folder belongs to pulse, so purge never deletes a folder
// someone pointed it at by mistake.
var (
	configMarkers = []string{"pulse.yaml"}
	dataMarkers   = []string{"state.json", "history.jsonl", "heartbeats"}
)

// Purge deletes the config and data folders. A folder that does not exist is
// skipped; one that does not look like pulse's is refused.
func Purge(configDir, dataDir string) ([]string, error) {
	var steps []string
	for _, d := range []struct {
		label, dir string
		markers    []string
	}{{"data", dataDir, dataMarkers}, {"configuration", configDir, configMarkers}} {
		if err := CheckDir(d.label, d.dir); err != nil {
			return steps, err
		}
		entries, err := os.ReadDir(d.dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return steps, fmt.Errorf("read %s: %w", d.dir, err)
		}
		if len(entries) > 0 && !hasAny(d.dir, d.markers) {
			return steps, fmt.Errorf("%s does not look like a pulse %s folder (no %s), so it was not deleted",
				d.dir, d.label, strings.Join(d.markers, " or "))
		}
		if err := os.RemoveAll(d.dir); err != nil {
			return steps, fmt.Errorf("delete %s: %w", d.dir, err)
		}
		steps = append(steps, "Deleted "+d.dir)
	}
	return steps, nil
}

func hasAny(dir string, names []string) bool {
	for _, n := range names {
		if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}
