// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

var (
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
)

const tcpTableOwnerPIDListener = 3

// Inventory lists running, automatically started services that are not part
// of Windows itself, and listening TCP ports.
func (Platform) Inventory(ctx context.Context) (host.Inventory, error) {
	var inv host.Inventory
	inv.Services = thirdPartyServices()
	for _, family := range []uint32{windows.AF_INET, windows.AF_INET6} {
		inv.Ports = append(inv.Ports, listeningPorts(family)...)
	}
	inv.Ports = dedupe(inv.Ports)
	return inv, ctx.Err()
}

func thirdPartyServices() []string {
	m, err := mgr.Connect()
	if err != nil {
		return nil
	}
	defer m.Disconnect()
	names, err := m.ListServices()
	if err != nil {
		return nil
	}
	sysRoot := strings.ToLower(os.Getenv("SystemRoot"))
	if sysRoot == "" {
		sysRoot = `c:\windows`
	}
	var out []string
	for _, n := range names {
		s, err := m.OpenService(n)
		if err != nil {
			continue
		}
		st, qerr := s.Query()
		cfg, cerr := s.Config()
		s.Close()
		if qerr != nil || cerr != nil || st.State != svc.Running || cfg.StartType != mgr.StartAutomatic ||
			strings.EqualFold(n, host.ServiceName) {
			continue
		}
		bin := strings.ToLower(strings.Trim(cfg.BinaryPathName, `" `))
		if strings.HasPrefix(bin, sysRoot+`\`) || strings.Contains(bin, `\windows defender\`) {
			continue // part of Windows
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func listeningPorts(family uint32) []host.ListenPort {
	var size uint32
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if size == 0 || size > 16<<20 {
		return nil
	}
	buf := make([]byte, size)
	if r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
		uintptr(family), tcpTableOwnerPIDListener, 0); r != 0 {
		return nil
	}
	return parseTCPTable(buf, family == windows.AF_INET6)
}

// parseTCPTable reads MIB_TCPTABLE_OWNER_PID or MIB_TCP6TABLE_OWNER_PID.
func parseTCPTable(buf []byte, v6 bool) []host.ListenPort {
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf))
	rowSize, addrOff, portOff := 24, 4, 8 // state, local addr, local port, remote addr, remote port, pid
	if v6 {
		rowSize, addrOff, portOff = 56, 0, 20 // local addr[16], scope, local port, remote..., state, pid
	}
	var out []host.ListenPort
	for i := 0; i < n; i++ {
		row := buf[4+i*rowSize:]
		if len(row) < rowSize {
			break
		}
		var ip net.IP
		if v6 {
			ip = net.IP(append([]byte(nil), row[addrOff:addrOff+16]...))
		} else {
			ip = net.IPv4(row[addrOff], row[addrOff+1], row[addrOff+2], row[addrOff+3])
		}
		port := int(binary.BigEndian.Uint16(row[portOff : portOff+2]))
		out = append(out, host.ListenPort{Address: ip.String(), Port: port})
	}
	return out
}

func dedupe(in []host.ListenPort) []host.ListenPort {
	best := map[int]host.ListenPort{}
	for _, p := range in {
		cur, seen := best[p.Port]
		if !seen || (p.Address == "0.0.0.0" || p.Address == "::") && cur.Address != "0.0.0.0" && cur.Address != "::" {
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
