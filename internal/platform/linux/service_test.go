// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package linux

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

func TestUnitFile(t *testing.T) {
	unit, err := unitFile("/usr/local/bin/pulse", "/etc/brynko-pulse", "/var/lib/brynko-pulse")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"User=brynko-pulse",
		"ExecStart=/usr/local/bin/pulse run --config /etc/brynko-pulse --data /var/lib/brynko-pulse",
		"ExecReload=/bin/kill -HUP $MAINPID",
		"ProtectSystem=strict",
		"ReadWritePaths=/var/lib/brynko-pulse",
		"NoNewPrivileges=yes",
		"CapabilityBoundingSet=\n",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit is missing %q", want)
		}
	}
	for _, bad := range []string{"/etc/my pulse", "/etc/%h", "/etc/$X", "/etc/a\"b", "relative"} {
		if _, err := unitFile("/usr/local/bin/pulse", bad, "/var/lib/brynko-pulse"); err == nil {
			t.Errorf("path %q should be refused", bad)
		}
	}
}

func TestShareConfigAndPrepareData(t *testing.T) {
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "pulse.yaml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gid, uid := os.Getgid(), os.Getuid()
	if err := shareConfig(cfg, gid); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(cfg, "pulse.yaml")); fi.Mode().Perm() != 0o640 {
		t.Errorf("config file mode %v, want 0640 (group may read, nobody else)", fi.Mode().Perm())
	}

	data := filepath.Join(t.TempDir(), "data")
	if err := prepareData(data, uid, gid); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(data, "heartbeats"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o770 || fi.Mode()&fs.ModeSetgid == 0 {
		t.Errorf("heartbeats mode %v, want group-writable with setgid", fi.Mode())
	}
}

func TestCheckSecretFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secrets.env")
	var pl Platform
	if err := pl.CheckSecretFile(p); err != nil {
		t.Fatalf("a missing file is fine: %v", err)
	}
	for mode, ok := range map[os.FileMode]bool{0o600: true, 0o640: true, 0o644: false, 0o660: false, 0o604: false} {
		if err := os.WriteFile(p, []byte("A=1"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if err := pl.CheckSecretFile(p); (err == nil) != ok {
			t.Errorf("mode %v: err %v, want ok=%v", mode, err, ok)
		}
	}
}

func TestBusPath(t *testing.T) {
	for unit, want := range map[string]string{
		"nginx.service":        "/org/freedesktop/systemd1/unit/nginx_2eservice",
		"2fa-web@1.service":    "/org/freedesktop/systemd1/unit/_32fa_2dweb_401_2eservice",
		unitName("postgresql"): "/org/freedesktop/systemd1/unit/postgresql_2eservice",
	} {
		if got := string(busPath(unit)); got != want {
			t.Errorf("busPath(%q) = %s, want %s", unit, got, want)
		}
	}
}

func TestActiveStateChanged(t *testing.T) {
	sig := func(iface string, props map[string]dbus.Variant) *dbus.Signal {
		return &dbus.Signal{Body: []any{iface, props, []string{}}}
	}
	if !activeStateChanged(sig("org.freedesktop.systemd1.Unit", map[string]dbus.Variant{"ActiveState": dbus.MakeVariant("failed")})) {
		t.Error("ActiveState change must count")
	}
	if activeStateChanged(sig("org.freedesktop.systemd1.Service", map[string]dbus.Variant{"CPUUsageNSec": dbus.MakeVariant(1)})) {
		t.Error("counters must not count")
	}
}

func TestParseProcAddr(t *testing.T) {
	for in, want := range map[string]string{
		"0100007F:1F90":                         "127.0.0.1:8080",
		"00000000:0016":                         "0.0.0.0:22",
		"00000000000000000000000000000000:01BB": ":::443",
		"00000000000000000000000001000000:1538": "::1:5432",
	} {
		a, p, ok := parseProcAddr(in)
		if !ok || a+":"+strconv.Itoa(p) != want {
			t.Errorf("%s = %s:%d %v, want %s", in, a, p, ok, want)
		}
	}
}

func TestDedupePorts(t *testing.T) {
	got := dedupePorts([]host.ListenPort{{Address: "127.0.0.1", Port: 80}, {Address: "0.0.0.0", Port: 80}, {Address: "127.0.0.53", Port: 53}, {Address: "::", Port: 22}})
	if len(got) != 2 || got[0].Port != 22 || got[1].Address != "0.0.0.0" {
		t.Fatalf("got %+v", got)
	}
	if !isPlumbing("systemd-journald") || isPlumbing("nginx") {
		t.Fatal("plumbing filter")
	}
}
