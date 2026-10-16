// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package linux

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// Filesystems that are not storage: kernel views, memory, containers' layers and packages.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "tmpfs": true, "devtmpfs": true, "devpts": true, "cgroup": true, "cgroup2": true,
	"overlay": true, "squashfs": true, "mqueue": true, "securityfs": true, "debugfs": true, "tracefs": true,
	"fusectl": true, "configfs": true, "pstore": true, "bpf": true, "autofs": true, "hugetlbfs": true,
	"binfmt_misc": true, "nsfs": true, "efivarfs": true, "ramfs": true, "rpc_pipefs": true, "fuse.lxcfs": true,
	"fuse.gvfsd-fuse": true, "fuse.portal": true, "selinuxfs": true, "nfsd": true,
}

// Places that only hold system or container internals, never the owner's data.
var internalPrefixes = []string{"/proc", "/sys", "/dev", "/run", "/snap", "/var/lib/docker", "/var/lib/containers",
	"/var/snap", "/boot/efi"}

// mountPoint is one line of /proc/self/mounts.
type mountPoint struct {
	Path   string
	Type   string
	Device string // the device's name without /dev/ (nvme0n1p1); empty when it is not a device
}

// parseMounts reads /proc/self/mounts and keeps the filesystems that hold data,
// once each, in the order the kernel lists them.
func parseMounts(r io.Reader) []mountPoint {
	var out []mountPoint
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		path, typ := unescapeMount(f[1]), f[2]
		if pseudoFS[typ] || seen[path] || isInternalPath(path) {
			continue
		}
		seen[path] = true
		out = append(out, mountPoint{Path: path, Type: typ, Device: deviceName(unescapeMount(f[0]))})
	}
	return out
}

// deviceName shortens /dev/nvme0n1p1 to nvme0n1p1 and /dev/mapper/vg-data to vg-data; a source
// that is not a device (a network share, overlay) gives "".
func deviceName(src string) string {
	if !strings.HasPrefix(src, "/dev/") {
		return ""
	}
	name := src[strings.LastIndex(src, "/")+1:]
	if name == "root" { // the kernel's alias for the boot disk says nothing more
		return ""
	}
	return name
}

func isInternalPath(p string) bool {
	for _, pre := range internalPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// unescapeMount decodes the octal escapes the kernel writes for spaces, tabs and
// backslashes in mount paths ("/mnt/my\040disk").
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
