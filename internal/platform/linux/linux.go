// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

// Package linux implements the platform for Linux.
package linux

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

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

// DiskUsage reports the filesystem holding path, the way df does.
func (Platform) DiskUsage(path string) (host.Disk, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return host.Disk{}, fmt.Errorf("read disk usage of %s: %w", path, err)
	}
	bs := uint64(s.Bsize)
	d := host.Disk{
		TotalBytes:      s.Blocks * bs,
		FreeBytes:       s.Bavail * bs,
		UsedBytes:       (s.Blocks - s.Bfree) * bs,
		InodesTotal:     s.Files,
		InodesFree:      s.Ffree,
		HasInodeDetails: s.Files > 0,
	}
	return d, nil
}

// Memory reads /proc/meminfo.
func (Platform) Memory() (host.Memory, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return host.Memory{}, err
	}
	defer f.Close()
	kb := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 {
			if n, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				kb[strings.TrimSuffix(fields[0], ":")] = n
			}
		}
	}
	if kb["MemTotal"] == 0 {
		return host.Memory{}, errors.New("/proc/meminfo has no MemTotal")
	}
	return host.Memory{
		TotalBytes:     kb["MemTotal"] * 1024,
		AvailableBytes: kb["MemAvailable"] * 1024,
		SwapTotalBytes: kb["SwapTotal"] * 1024,
		SwapFreeBytes:  kb["SwapFree"] * 1024,
	}, sc.Err()
}

// CPUTimes reads the aggregate line of /proc/stat.
func (Platform) CPUTimes() (host.CPUTimes, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return host.CPUTimes{}, err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return host.CPUTimes{}, errors.New("unexpected /proc/stat format")
	}
	var t host.CPUTimes
	for i, f := range fields[1:] {
		if i >= 8 { // guest time is already counted in user time
			break
		}
		n, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return host.CPUTimes{}, fmt.Errorf("parse /proc/stat: %w", err)
		}
		t.Total += n
		if i == 3 || i == 4 { // idle, iowait
			t.Idle += n
		}
	}
	return t, nil
}

// Uptime reads /proc/uptime.
func (Platform) Uptime() (time.Duration, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	return host.ParseUptime(string(data))
}

// LoadAverage reads /proc/loadavg.
func (Platform) LoadAverage() (host.Load, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return host.Load{}, err
	}
	f := strings.Fields(string(data))
	if len(f) < 3 {
		return host.Load{}, errors.New("unexpected /proc/loadavg format")
	}
	var l host.Load
	for i, dst := range []*float64{&l.Load1, &l.Load5, &l.Load15} {
		if *dst, err = strconv.ParseFloat(f[i], 64); err != nil {
			return host.Load{}, err
		}
	}
	l.CPUs = runtime.NumCPU()
	return l, nil
}

// Service asks systemd about a unit.
func (Platform) Service(ctx context.Context, name string) (host.Service, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return host.Service{}, fmt.Errorf("invalid service name %q", name)
	}
	out, err := exec.CommandContext(ctx, "systemctl", "show",
		"--property=LoadState,ActiveState,SubState,NRestarts", "--", name).Output()
	if err != nil {
		return host.Service{}, fmt.Errorf("ask systemd about %s: %w", name, err)
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = strings.TrimSpace(v)
		}
	}
	if props["LoadState"] == "not-found" {
		return host.Service{}, fmt.Errorf("service %s: %w", name, host.ErrNotFound)
	}
	restarts := -1
	if n, err := strconv.Atoi(props["NRestarts"]); err == nil {
		restarts = n
	}
	state := props["SubState"]
	if state == "" {
		state = props["ActiveState"]
	}
	return host.Service{Active: props["ActiveState"] == "active", State: state, Restarts: restarts}, nil
}

// ProcessCount counts processes whose name equals match or whose command line contains it.
func (Platform) ProcessCount(match string) (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	self := strconv.Itoa(os.Getpid())
	count := 0
	for _, e := range entries {
		pid := e.Name()
		if !e.IsDir() || pid == self || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join("/proc", pid, "comm"))
		if strings.TrimSpace(string(comm)) == match {
			count++
			continue
		}
		cmd, _ := os.ReadFile(filepath.Join("/proc", pid, "cmdline"))
		if len(cmd) > 0 && bytes.Contains(bytes.ReplaceAll(cmd, []byte{0}, []byte{' '}), []byte(match)) {
			count++
		}
	}
	return count, nil
}

// CheckConfigSecure refuses configuration that someone other than root or the
// owner of this process could change.
func (Platform) CheckConfigSecure(dir string) error {
	paths := []string{dir}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && p != dir && (d.IsDir() || strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")) {
			paths = append(paths, p)
		}
		return nil
	})
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		if fi.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s can be changed by other users (mode %v). Fix it with: chmod go-w %s", p, fi.Mode().Perm(), p)
		}
		if err := checkOwner(p, fi); err != nil {
			return err
		}
	}
	return nil
}

// CheckSecretFile refuses a secrets file that other users could read or change.
// The service group may read it.
func (Platform) CheckSecretFile(path string) error {
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o037 != 0 {
		return fmt.Errorf("%s holds secrets but other users can read or change it (mode %v). Fix it with: chmod 640 %s", path, fi.Mode().Perm(), path)
	}
	return checkOwner(path, fi)
}

func checkOwner(p string, fi os.FileInfo) error {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("%s is owned by another user (uid %d). It must belong to root", p, st.Uid)
	}
	return nil
}

// SecureConfig is a no-op: Init already creates files with safe modes.
func (Platform) SecureConfig(string) error { return nil }
