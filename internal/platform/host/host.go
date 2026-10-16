// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package host defines the measurements every platform provides.
package host

import "errors"

// ErrUnsupported means the measurement does not exist on this operating system.
var ErrUnsupported = errors.New("not available on this operating system")

// ErrNotFound means the named service or process does not exist.
var ErrNotFound = errors.New("not found")

// Disk is the usage of one filesystem or drive.
type Disk struct {
	TotalBytes      uint64
	FreeBytes       uint64 // available to ordinary users
	UsedBytes       uint64
	InodesTotal     uint64 // zero when unknown
	InodesFree      uint64
	HasInodeDetails bool
}

// Memory is physical memory and swap.
type Memory struct {
	TotalBytes     uint64
	AvailableBytes uint64
	SwapTotalBytes uint64
	SwapFreeBytes  uint64
}

// CPUTimes are cumulative counters; usage comes from the difference between two samples.
type CPUTimes struct {
	Idle  uint64
	Total uint64
}

// Load is the run-queue average.
type Load struct {
	Load1, Load5, Load15 float64
	CPUs                 int
}

// Service is the state of a system service.
type Service struct {
	Active   bool
	State    string // as the OS reports it, for example "running" or "failed"
	Restarts int    // -1 when the OS does not report it
}

// ServiceWatcher calls back when a watched service changes state, so it can be
// checked at once instead of at its next interval. It stops when the context
// it was created with ends.
type ServiceWatcher interface {
	Watch(name string, changed func())
}

// Inventory is what runs on this server, for pulse discover.
type Inventory struct {
	Services    []string     // running services worth watching, system plumbing left out
	Ports       []ListenPort // TCP ports something listens on
	Filesystems []Filesystem // filesystems that hold data, pseudo and system ones left out
}

// Filesystem is one mounted filesystem (a drive on Windows) a disk monitor can watch.
type Filesystem struct {
	Path   string // mount point, or a drive root on Windows
	Type   string // ext4, xfs, NTFS...
	Device string // nvme0n1p1, sdb1, or the volume label on Windows; may be empty
	Disk   Disk
}

// ListenPort is one listening TCP socket.
type ListenPort struct {
	Address string // the address it is bound to; empty or a wildcard means every address
	Port    int
}
