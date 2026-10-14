// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package linux implements the platform for Linux.
package linux

// Platform is the Linux implementation.
type Platform struct{}

// Name returns "linux".
func (Platform) Name() string { return "linux" }

// ConfigDir is where pulse.yaml, channels.yaml and monitors/ live.
func (Platform) ConfigDir() string { return "/etc/brynko-pulse" }

// DataDir holds state and alert history.
func (Platform) DataDir() string { return "/var/lib/brynko-pulse" }

// RootDisk is the root filesystem.
func (Platform) RootDisk() string { return "/" }
