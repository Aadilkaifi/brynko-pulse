// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

// Package windows implements the platform for Windows.
package windows

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
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

// DiskUsage reports the drive holding path.
func (Platform) DiskUsage(path string) (host.Disk, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return host.Disk{}, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return host.Disk{}, fmt.Errorf("read disk usage of %s: %w", path, err)
	}
	return host.Disk{TotalBytes: total, FreeBytes: avail, UsedBytes: total - free}, nil
}

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// Memory reports physical memory, and the page file as swap.
func (Platform) Memory() (host.Memory, error) {
	m := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return host.Memory{}, fmt.Errorf("GlobalMemoryStatusEx: %w", err)
	}
	// Swap is the page files themselves. GlobalMemoryStatusEx's page-file figures are the commit limit
	// and charge (memory plus page files), which reads as a full page file once commitments pass free
	// memory; a server that cannot answer is treated as having no page file.
	swapTotal, swapUsed, err := pageFiles()
	if err != nil {
		swapTotal, swapUsed = 0, 0
	}
	return host.Memory{TotalBytes: m.TotalPhys, AvailableBytes: m.AvailPhys,
		SwapTotalBytes: swapTotal, SwapFreeBytes: swapTotal - min(swapUsed, swapTotal)}, nil
}

// pageFileInfo is one SYSTEM_PAGEFILE_INFORMATION entry; sizes are in pages.
type pageFileInfo struct {
	NextEntryOffset uint32
	TotalSize       uint32
	TotalInUse      uint32
	PeakUsage       uint32
	Name            windows.NTUnicodeString
}

// pageFiles reports the size and use of every page file, as Task Manager and Win32_PageFileUsage do.
func pageFiles() (total, used uint64, err error) {
	buf := make([]byte, 1024)
	var n uint32
	for {
		err = windows.NtQuerySystemInformation(windows.SystemPageFileInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)), &n)
		if err != windows.STATUS_INFO_LENGTH_MISMATCH || len(buf) >= 1<<16 {
			break
		}
		buf = make([]byte, len(buf)*4)
	}
	if err != nil || n == 0 {
		return 0, 0, err // n == 0: no page file
	}
	page := uint64(os.Getpagesize())
	for off := uint32(0); int(off)+int(unsafe.Sizeof(pageFileInfo{})) <= len(buf); {
		e := (*pageFileInfo)(unsafe.Pointer(&buf[off]))
		total += uint64(e.TotalSize) * page
		used += uint64(e.TotalInUse) * page
		if e.NextEntryOffset == 0 {
			break
		}
		off += e.NextEntryOffset
	}
	return total, used, nil
}

// CPUTimes uses GetSystemTimes; kernel time already includes idle time.
func (Platform) CPUTimes() (host.CPUTimes, error) {
	var idle, kernel, user windows.Filetime
	if r, _, err := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); r == 0 {
		return host.CPUTimes{}, fmt.Errorf("GetSystemTimes: %w", err)
	}
	ft := func(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
	return host.CPUTimes{Idle: ft(idle), Total: ft(kernel) + ft(user)}, nil
}

// LoadAverage does not exist on Windows.
func (Platform) LoadAverage() (host.Load, error) {
	return host.Load{CPUs: runtime.NumCPU()}, fmt.Errorf("load average: %w; use a cpu monitor instead", host.ErrUnsupported)
}

// Service asks the Service Control Manager.
func (Platform) Service(_ context.Context, name string) (host.Service, error) {
	// Query rights only: the service account is not an administrator (mgr.Connect would ask for full control).
	s, done, err := openService(name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return host.Service{}, err
	}
	defer done()
	st, err := s.Query()
	if err != nil {
		return host.Service{}, fmt.Errorf("query service %s: %w", name, err)
	}
	states := map[svc.State]string{svc.Stopped: "stopped", svc.StartPending: "starting",
		svc.StopPending: "stopping", svc.Running: "running", svc.ContinuePending: "resuming",
		svc.PausePending: "pausing", svc.Paused: "paused"}
	return host.Service{Active: st.State == svc.Running, State: states[st.State], Restarts: -1}, nil
}

// ProcessCount counts processes whose executable name matches, with or without ".exe".
func (Platform) ProcessCount(match string) (int, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snap)
	want := strings.TrimSuffix(strings.ToLower(match), ".exe")
	self := uint32(os.Getpid())
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	count := 0
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		name := strings.TrimSuffix(strings.ToLower(windows.UTF16ToString(e.ExeFile[:])), ".exe")
		if name == want && e.ProcessID != self {
			count++
		}
	}
	return count, nil
}

// Rights that let someone change a file or folder.
const writeRights = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.GENERIC_WRITE |
	windows.GENERIC_ALL | windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE |
	windows.FILE_WRITE_ATTRIBUTES | windows.FILE_WRITE_EA

// Rights that let someone read a file's contents.
const readRights = windows.FILE_READ_DATA | windows.GENERIC_READ | windows.GENERIC_ALL

// Folder permissions: SYSTEM and Administrators get full control. The service
// account is added by install: read only on the config, full on its data.
const (
	adminOnlySDDL  = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	serviceReadACE = "(A;OICI;FR;;;%s)"
	serviceFullACE = "(A;OICI;FA;;;%s)"
)

// CheckConfigSecure refuses configuration that anyone other than SYSTEM,
// Administrators, a service account or the user running pulse may change.
func (Platform) CheckConfigSecure(dir string) error {
	paths := []string{dir}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && p != dir && (d.IsDir() || strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")) {
			paths = append(paths, p)
		}
		return nil
	})
	for _, p := range paths {
		if who, err := grantedToOthers(p, writeRights); err != nil {
			return err
		} else if who != "" {
			return fmt.Errorf("%s can be changed by %s. Only SYSTEM, Administrators and the user running pulse may change it; run pulse init again or fix its permissions", p, who)
		}
	}
	return nil
}

// CheckSecretFile refuses a secrets file that anyone other than SYSTEM,
// Administrators, a service account or the user running pulse may read or change.
func (Platform) CheckSecretFile(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if who, err := grantedToOthers(path, readRights|writeRights); err != nil {
		return err
	} else if who != "" {
		return fmt.Errorf("%s holds secrets but %s can read or change it. Only SYSTEM, Administrators and the user running pulse may; fix its permissions", path, who)
	}
	return nil
}

func grantedToOthers(path string, rights windows.ACCESS_MASK) (string, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", fmt.Errorf("read permissions of %s: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return "", err
	}
	if dacl == nil {
		return "everyone", nil // a null DACL grants everyone full access
	}
	// As on Linux, where the user running pulse may own its files.
	me, err := currentUser()
	if err != nil {
		return "", err
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return "", err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if ace.Mask&rights == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) ||
			strings.HasPrefix(sid.String(), "S-1-5-80-") || // service SIDs such as TrustedInstaller
			windows.EqualSid(sid, me) {
			continue
		}
		return sid.String(), nil
	}
	return "", nil
}

// SecureConfig gives only SYSTEM and Administrators access to the config folder.
// Without elevation the user running pulse keeps access too; otherwise they would
// lock themselves out of a folder they chose with --config.
func (p Platform) SecureConfig(dir string) error {
	sddl := adminOnlySDDL
	if !windows.GetCurrentProcessToken().IsElevated() {
		me, err := currentUser()
		if err != nil {
			return err
		}
		sddl += fmt.Sprintf(serviceFullACE, me.String())
	}
	return setTreeACL(dir, sddl, p.DataDir())
}

func currentUser() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read the current user: %w", err)
	}
	return u.User.Sid, nil
}

// setTreeACL sets a protected DACL on dir and makes everything below inherit
// it, except the skip folder, which keeps its own permissions.
func setTreeACL(dir, sddl, skip string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		return fmt.Errorf("set permissions on %s: %w", dir, err)
	}
	// Everything below keeps only what it inherits from dir. That takes an empty ACL: a nil one would
	// be a NULL DACL, which gives everyone full control.
	empty, err := windows.SecurityDescriptorFromString("D:")
	if err != nil {
		return err
	}
	inherit, _, err := empty.DACL()
	if err != nil {
		return err
	}
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		if skip != "" && d.IsDir() && strings.EqualFold(filepath.Clean(p), filepath.Clean(skip)) {
			return filepath.SkipDir
		}
		if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, inherit, nil); err != nil {
			return fmt.Errorf("set permissions on %s: %w", p, err)
		}
		return nil
	})
}
