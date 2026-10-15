// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/fsutil"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

const (
	binaryPath     = "/usr/local/bin/pulse"
	packagedBinary = "/usr/bin/pulse" // where the .deb and .rpm put it
	unitPath       = "/etc/systemd/system/" + host.ServiceName + ".service"
	serviceUser    = host.ServiceName
	dockerGroup    = "docker"
	journalGroup   = "systemd-journal"
)

// Paths systemd can take without quoting or specifier surprises.
var safeUnitPath = regexp.MustCompile(`^/[A-Za-z0-9._/@+:-]+$`)

// Install creates the service user, copies the binary, gives the service read
// access to the config and write access to the data folder, and starts the
// systemd unit. Running it again updates everything in place.
func (p Platform) Install(ctx context.Context, o host.InstallOptions) (host.InstallResult, error) {
	bin := installedBinary(o.Source)
	res := host.InstallResult{Binary: bin, Service: host.ServiceName, Account: serviceUser,
		Steps: []string{}, Warnings: []string{},
		Next: []string{
			"See what it is watching: pulse status",
			"Follow its log: journalctl -u " + host.ServiceName + " -f",
			"After changing the configuration: sudo systemctl reload " + host.ServiceName,
		}}
	step := func(s string) { res.Steps = append(res.Steps, s) }
	if err := preflight(); err != nil {
		return res, err
	}
	unit, err := unitFile(bin, o.ConfigDir, o.DataDir)
	if err != nil {
		return res, err
	}

	u, created, err := ensureUser(ctx, o.DataDir)
	if err != nil {
		return res, err
	}
	if created {
		step("Created the system user " + serviceUser + " (no login, no home folder)")
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)

	if msg, err := setDockerAccess(ctx, u, o.AllowDocker); err != nil {
		return res, err
	} else if msg != "" {
		step(msg)
	}
	if msg, err := setGroup(ctx, u, journalGroup, o.JournalMonitors > 0,
		"Let "+serviceUser+" read the systemd journal, for journal log monitors (group "+journalGroup+")",
		"Removed journal access from "+serviceUser+": no journal log monitors are left"); err != nil {
		return res, err
	} else if msg != "" {
		step(msg)
	}
	for _, f := range unreadable(o.LogFiles, u) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s may not be readable by %s. Let it read the file, for example: sudo usermod -aG adm %s",
			f, serviceUser, serviceUser))
	}
	if o.ContainerMonitors > 0 && !o.AllowDocker {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d container monitor(s) cannot reach Docker because of --no-docker. Run pulse install without it to allow them.", o.ContainerMonitors))
	}

	if bin == packagedBinary {
		// A copy from an earlier manual install would come first in PATH.
		if err := os.Remove(binaryPath); err == nil {
			step("Removed the older copy at " + binaryPath + "; the package's " + packagedBinary + " is used")
		}
	} else {
		copied, err := fsutil.CopyAtomic(o.Source, binaryPath, 0o755)
		if err != nil {
			return res, fmt.Errorf("copy pulse to %s: %w", binaryPath, err)
		}
		if copied {
			step("Copied pulse to " + binaryPath)
		}
	}
	if err := shareConfig(o.ConfigDir, gid); err != nil {
		return res, err
	}
	step("Let the service read " + o.ConfigDir + " (group " + serviceUser + ", read only)")
	if err := prepareData(o.DataDir, uid, gid); err != nil {
		return res, err
	}
	step("Gave the service its data folder " + o.DataDir)

	if err := fsutil.WriteAtomic(unitPath, []byte(unit), 0o644); err != nil {
		return res, err
	}
	step("Wrote " + unitPath)
	for _, args := range [][]string{{"daemon-reload"}, {"enable", host.ServiceName}, {"restart", host.ServiceName}} {
		if err := runTool(ctx, "systemctl", args...); err != nil {
			return res, err
		}
	}
	step("Enabled and started " + host.ServiceName + "; it starts again after a reboot")
	return res, p.waitActive(ctx)
}

// Uninstall stops and removes the unit and the binary. Config and data stay;
// with Purge the service user is removed as well.
func (Platform) Uninstall(ctx context.Context, o host.UninstallOptions) (host.UninstallResult, error) {
	res := host.UninstallResult{Steps: []string{}}
	if os.Geteuid() != 0 {
		return res, fmt.Errorf("uninstall: %w. Run it with sudo", host.ErrNotAdmin)
	}
	if _, err := os.Stat(unitPath); err == nil {
		if err := runTool(ctx, "systemctl", "disable", "--now", host.ServiceName); err != nil {
			return res, err
		}
		if err := os.Remove(unitPath); err != nil {
			return res, err
		}
		if err := runTool(ctx, "systemctl", "daemon-reload"); err != nil {
			return res, err
		}
		res.Steps = append(res.Steps, "Stopped and removed the "+host.ServiceName+" service")
	}
	if err := os.Remove(binaryPath); err == nil {
		res.Steps = append(res.Steps, "Removed "+binaryPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	if o.Purge {
		if _, err := user.Lookup(serviceUser); err == nil {
			if err := runTool(ctx, "userdel", serviceUser); err != nil {
				return res, err
			}
			res.Steps = append(res.Steps, "Removed the system user "+serviceUser)
		}
	}
	return res, nil
}

// ReloadService sends the running unit a reload, which pulse handles without a restart.
func (Platform) ReloadService(ctx context.Context) (bool, error) {
	if _, err := os.Stat(unitPath); err != nil {
		return false, nil
	}
	if runTool(ctx, "systemctl", "is-active", "--quiet", host.ServiceName) != nil {
		return false, nil
	}
	return true, runTool(ctx, "systemctl", "reload", host.ServiceName)
}

// RunAsService reports false: systemd runs `pulse run` as a normal process and
// stops or reloads it with signals.
func (Platform) RunAsService(host.ServiceFunc) (bool, error) { return false, nil }

// installedBinary is where the service runs pulse from: the package's copy
// when the package manager installed it, otherwise our own copy.
func installedBinary(source string) string {
	if source == packagedBinary {
		return packagedBinary
	}
	return binaryPath
}

func preflight() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("install: %w. Run it with sudo", host.ErrNotAdmin)
	}
	if fi, err := os.Stat("/run/systemd/system"); err != nil || !fi.IsDir() {
		return errors.New("this system does not use systemd, which pulse install needs. Start `pulse run` from your init system instead")
	}
	return nil
}

func unitFile(bin, configDir, dataDir string) (string, error) {
	for _, d := range []string{bin, configDir, dataDir} {
		if !safeUnitPath.MatchString(d) {
			return "", fmt.Errorf("the path %q has characters a systemd unit cannot take safely. Use letters, digits and . _ - / only", d)
		}
	}
	return fmt.Sprintf(`# Written by pulse install. Run pulse install again to update it.
[Unit]
Description=brynko-pulse server monitoring and alerts
Documentation=https://github.com/Aadilkaifi/brynko-pulse
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=%[4]s
Group=%[4]s
ExecStart=%[1]s run --config %[2]s --data %[3]s
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5s
UMask=0027

# Least privilege: no extra rights, read-only system, writes only to the data folder.
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%[3]s
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes
RestrictRealtime=yes
LockPersonality=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK

[Install]
WantedBy=multi-user.target
`, bin, configDir, dataDir, serviceUser), nil
}

func ensureUser(ctx context.Context, home string) (*user.User, bool, error) {
	if u, err := user.Lookup(serviceUser); err == nil {
		return u, false, nil
	}
	if _, err := exec.LookPath("useradd"); err != nil {
		return nil, false, fmt.Errorf("useradd was not found. Create a system user and group named %s, then run pulse install again", serviceUser)
	}
	err := runTool(ctx, "useradd", "--system", "--user-group", "--no-create-home",
		"--home-dir", home, "--shell", nologin(), serviceUser)
	if err != nil {
		return nil, false, err
	}
	u, err := user.Lookup(serviceUser)
	return u, true, err
}

func nologin() string {
	for _, p := range []string{"/usr/sbin/nologin", "/sbin/nologin"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/bin/false"
}

// setDockerAccess makes docker group membership match the request, so running
// install again with --no-docker takes the access away. Without Docker it does nothing.
func setDockerAccess(ctx context.Context, u *user.User, allow bool) (string, error) {
	return setGroup(ctx, u, dockerGroup, allow,
		"Gave "+serviceUser+" access to Docker (membership of the docker group is equivalent to root)",
		"Removed Docker access from "+serviceUser)
}

// setGroup makes membership of group match want. A group that does not exist
// is left alone.
func setGroup(ctx context.Context, u *user.User, group string, want bool, added, removed string) (string, error) {
	g, err := user.LookupGroup(group)
	if err != nil {
		return "", nil
	}
	groups, _ := u.GroupIds()
	member := slices.Contains(groups, g.Gid)
	switch {
	case want && !member:
		if err := runTool(ctx, "usermod", "--append", "--groups", group, serviceUser); err != nil {
			return "", err
		}
		return added, nil
	case !want && member:
		if err := runTool(ctx, "gpasswd", "--delete", serviceUser, group); err != nil {
			return "", err
		}
		return removed, nil
	}
	return "", nil
}

// unreadable lists log files the service user will probably not be able to
// read: neither world-readable nor in a group it belongs to.
func unreadable(files []string, u *user.User) []string {
	groups, _ := u.GroupIds()
	var out []string
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			continue // may appear later
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if fi.Mode().Perm()&0o004 != 0 || ok && fi.Mode().Perm()&0o040 != 0 && slices.Contains(groups, strconv.Itoa(int(st.Gid))) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// shareConfig gives the service group read access without changing the owner,
// so the configuration still belongs to root.
func shareConfig(dir string, gid int) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		want := fi.Mode().Perm() | 0o040
		if d.IsDir() {
			// setgid: files added later, such as by pulse add, get the service group too.
			want |= 0o010 | fs.ModeSetgid
		}
		if err := os.Lchown(p, -1, gid); err != nil {
			return fmt.Errorf("let the service read %s: %w", p, err)
		}
		return os.Chmod(p, want)
	})
}

// prepareData hands the data folder to the service user. Heartbeats can be
// recorded by anyone in the service group, such as a cron job's user.
func prepareData(dir string, uid, gid int) error {
	hb := filepath.Join(dir, "heartbeats")
	if err := os.MkdirAll(hb, 0o750); err != nil {
		return err
	}
	// setgid: files written by root, such as silences, stay readable by the service.
	if err := os.Chmod(dir, 0o750|fs.ModeSetgid); err != nil {
		return err
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
	if err != nil {
		return fmt.Errorf("give the service %s: %w", dir, err)
	}
	return os.Chmod(hb, 0o770|fs.ModeSetgid)
}

func (p Platform) waitActive(ctx context.Context) error {
	// A config the service user cannot read makes pulse exit at once, so give it
	// a moment, then check that it is still running.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}
	s, err := p.Service(ctx, host.ServiceName)
	if err != nil {
		return err
	}
	if !s.Active {
		return fmt.Errorf("the service did not stay running (state %s). See why with: journalctl -u %s -n 50", s.State, host.ServiceName)
	}
	return nil
}

const toolOutputLimit = 2 << 10

func runTool(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if len(msg) > toolOutputLimit {
		msg = msg[:toolOutputLimit] + "..."
	}
	if msg != "" {
		return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), msg)
	}
	return fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
}
