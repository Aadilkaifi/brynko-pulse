// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !linux && !windows

package platform

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// unsupported lets pulse build on other systems for development. It never monitors.
type unsupported struct{}

var current Platform = unsupported{}

var errOS = fmt.Errorf("pulse supports Linux and Windows: %w", host.ErrUnsupported)

func (unsupported) Name() string                        { return "unsupported" }
func (unsupported) ConfigDir() string                   { return "/etc/brynko-pulse" }
func (unsupported) DataDir() string                     { return "/var/lib/brynko-pulse" }
func (unsupported) RootDisk() string                    { return "/" }
func (unsupported) DiskUsage(string) (host.Disk, error) { return host.Disk{}, errOS }
func (unsupported) Memory() (host.Memory, error)        { return host.Memory{}, errOS }
func (unsupported) CPUTimes() (host.CPUTimes, error)    { return host.CPUTimes{}, errOS }
func (unsupported) LoadAverage() (host.Load, error)     { return host.Load{}, errOS }
func (unsupported) ProcessCount(string) (int, error)    { return 0, errOS }
func (unsupported) CheckConfigSecure(string) error      { return nil }
func (unsupported) SecureConfig(string) error           { return nil }
func (unsupported) CheckSecretFile(string) error        { return nil }
func (unsupported) NewServiceWatcher(context.Context, *slog.Logger) (host.ServiceWatcher, error) {
	return nil, errOS
}
func (unsupported) Inventory(context.Context) (host.Inventory, error)          { return host.Inventory{}, errOS }
func (unsupported) FollowEventLog(context.Context, string, func(string)) error { return errOS }
func (unsupported) ReloadService(context.Context) (bool, error)                { return false, nil }
func (unsupported) RunAsService(host.ServiceFunc) (bool, error) {
	return false, nil
}
func (unsupported) Install(context.Context, host.InstallOptions) (host.InstallResult, error) {
	return host.InstallResult{}, errOS
}
func (unsupported) Uninstall(context.Context, host.UninstallOptions) (host.UninstallResult, error) {
	return host.UninstallResult{}, errOS
}
func (unsupported) Service(context.Context, string) (host.Service, error) {
	return host.Service{}, errOS
}
