// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package platform is the only place that knows which operating system pulse runs on.
// The implementation is chosen at build time; shared code calls Current().
package platform

import (
	"context"
	"log/slog"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// Platform is implemented once per supported operating system.
type Platform interface {
	Name() string
	ConfigDir() string
	DataDir() string
	// RootDisk is the path of the system disk, for the starter disk monitor.
	RootDisk() string

	DiskUsage(path string) (host.Disk, error)
	Memory() (host.Memory, error)
	CPUTimes() (host.CPUTimes, error)
	LoadAverage() (host.Load, error)
	Service(ctx context.Context, name string) (host.Service, error)
	ProcessCount(match string) (int, error)
	// Inventory lists running services and listening ports, for pulse discover.
	Inventory(ctx context.Context) (host.Inventory, error)
	// FollowEventLog calls onLine for each new event in a Windows Event Log
	// channel until ctx ends. Other systems return host.ErrUnsupported.
	FollowEventLog(ctx context.Context, channel string, onLine func(string)) error
	// NewServiceWatcher reports service state changes as they happen, until ctx ends.
	NewServiceWatcher(ctx context.Context, log *slog.Logger) (host.ServiceWatcher, error)

	// CheckConfigSecure returns an error when people other than administrators
	// could change the configuration, since it can run commands.
	CheckConfigSecure(dir string) error
	// SecureConfig restricts the config folder to administrators after it is created.
	SecureConfig(dir string) error
	// CheckSecretFile returns an error when people other than administrators and
	// the pulse service could read or change the secrets file. A missing file is fine.
	CheckSecretFile(path string) error

	// Install sets pulse up as a background service that starts at boot.
	// Running it again updates the binary and the service in place.
	Install(ctx context.Context, o host.InstallOptions) (host.InstallResult, error)
	// Uninstall stops and removes the service and the installed binary.
	Uninstall(ctx context.Context, o host.UninstallOptions) (host.UninstallResult, error)
	// ReloadService asks the installed service to reread its configuration. It
	// reports false, without an error, when the service is not installed or running.
	ReloadService(ctx context.Context) (bool, error)
	// RunAsService runs fn under the system service manager when that is how
	// pulse was started, and reports whether it did.
	RunAsService(fn host.ServiceFunc) (bool, error)
}

// Current returns the platform this binary was built for.
func Current() Platform { return current }
