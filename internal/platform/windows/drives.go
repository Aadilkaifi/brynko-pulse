// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import (
	"golang.org/x/sys/windows"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// fixedDrives lists the local hard drives (not removable, network or optical ones),
// for pulse suggest.
func fixedDrives() []host.Filesystem {
	buf := make([]uint16, 512)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil || n == 0 || int(n) > len(buf) {
		return nil
	}
	var out []host.Filesystem
	for _, root := range splitNul(buf[:n]) {
		p, err := windows.UTF16PtrFromString(root)
		if err != nil || windows.GetDriveType(p) != windows.DRIVE_FIXED {
			continue
		}
		d, err := (Platform{}).DiskUsage(root)
		if err != nil || d.TotalBytes == 0 {
			continue
		}
		label := make([]uint16, windows.MAX_PATH+1)
		name := make([]uint16, windows.MAX_PATH+1)
		typ, vol := "", ""
		if windows.GetVolumeInformation(p, &label[0], uint32(len(label)), nil, nil, nil, &name[0], uint32(len(name))) == nil {
			typ, vol = windows.UTF16ToString(name), windows.UTF16ToString(label)
		}
		out = append(out, host.Filesystem{Path: root, Type: typ, Device: vol, Disk: d})
	}
	return out
}

// splitNul splits a list of strings separated by zeros, as GetLogicalDriveStrings returns it.
func splitNul(b []uint16) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c == 0 {
			if i > start {
				out = append(out, windows.UTF16ToString(b[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, windows.UTF16ToString(b[start:]))
	}
	return out
}
