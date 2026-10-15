// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package linux

import (
	"bufio"
	"context"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// Units that are part of the operating system rather than something the
// owner runs; suggesting monitors for them would only add noise.
var plumbing = []string{"systemd-", "dbus", "getty@", "serial-getty@", "user@", "user-runtime-dir@",
	"polkit", "udisks2", "accounts-daemon", "modemmanager", "networkmanager", "multipathd", "snapd",
	"packagekit", "unattended-upgrades", "irqbalance", "rsyslog", "networkd-dispatcher", "thermald",
	"upower", "wpa_supplicant", "avahi-daemon", "cups", "colord", "lvm2", "atd", "fwupd", "rtkit",
	"power-profiles", "switcheroo", "kerneloops", "whoopsie", "gdm", "lightdm", "bluetooth", "acpid",
	"serial-getty", "qemu-guest-agent", "open-vm-tools", "vgauth", "cloud-", "amazon-ssm", "snap.",
	"brynko-pulse", "uuidd", "auditd", "chronyd", "chrony", "ntp", "haveged", "smartd", "mdmonitor"}

// Inventory lists running services and listening TCP ports.
func (Platform) Inventory(ctx context.Context) (host.Inventory, error) {
	var inv host.Inventory
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "list-units", "--type=service", "--state=running",
		"--no-legend", "--plain", "--no-pager").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			name := strings.TrimSuffix(f[0], ".service")
			if !isPlumbing(name) {
				inv.Services = append(inv.Services, name)
			}
		}
		sort.Strings(inv.Services)
	}
	for _, file := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		ports, err := listening(file)
		if err == nil {
			inv.Ports = append(inv.Ports, ports...)
		}
	}
	inv.Ports = dedupePorts(inv.Ports)
	return inv, nil
}

func isPlumbing(name string) bool {
	low := strings.ToLower(name)
	for _, p := range plumbing {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// listening reads LISTEN sockets (state 0A) from a /proc/net/tcp file.
func listening(file string) ([]host.ListenPort, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []host.ListenPort
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		addr, port, ok := parseProcAddr(fields[1])
		if ok {
			out = append(out, host.ListenPort{Address: addr, Port: port})
		}
	}
	return out, sc.Err()
}

// parseProcAddr decodes "0100007F:1F90": the address in host byte order per
// 32-bit word, then the port in hex.
func parseProcAddr(s string) (string, int, bool) {
	a, p, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, false
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return "", 0, false
	}
	raw, err := hex.DecodeString(a)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", 0, false
	}
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		ip[i], ip[i+1], ip[i+2], ip[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	return ip.String(), int(port), true
}

// dedupePorts keeps one entry per port, preferring a wildcard bind, and drops
// the local DNS stub, which is system plumbing.
func dedupePorts(in []host.ListenPort) []host.ListenPort {
	best := map[int]host.ListenPort{}
	for _, p := range in {
		if strings.HasPrefix(p.Address, "127.0.0.53") || strings.HasPrefix(p.Address, "127.0.0.54") {
			continue
		}
		cur, seen := best[p.Port]
		if !seen || isWildcard(p.Address) && !isWildcard(cur.Address) {
			best[p.Port] = p
		}
	}
	out := make([]host.ListenPort, 0, len(best))
	for _, p := range best {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

func isWildcard(a string) bool { return a == "" || a == "0.0.0.0" || a == "::" }
