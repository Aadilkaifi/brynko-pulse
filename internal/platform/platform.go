// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package platform is the only place that knows which operating system pulse runs on.
// The implementation is chosen at build time; shared code calls Current().
package platform

// Platform is implemented once per supported operating system.
type Platform interface {
	Name() string
	ConfigDir() string
	DataDir() string
	// RootDisk is the path of the system disk, for the starter disk monitor.
	RootDisk() string
}

// Current returns the platform this binary was built for.
func Current() Platform { return current }
