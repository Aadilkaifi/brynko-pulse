// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package linux

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/Aadilkaifi/brynko-pulse/internal/fsutil"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// Self-healing restarts a unit through systemd's D-Bus API, which still works
// under the service's NoNewPrivileges. polkit decides; pulse's rule allows the
// service account to restart only the units that have heal: restart.
const (
	polkitRulesDir = "/etc/polkit-1/rules.d"
	polkitRule     = polkitRulesDir + "/60-" + host.ServiceName + ".rules"
)

// A unit name pulse is willing to write into a polkit rule.
var safeUnit = regexp.MustCompile(`^[A-Za-z0-9@._:-]+$`)

// RestartService restarts a systemd unit and waits for the restart job to finish.
func (Platform) RestartService(ctx context.Context, name string) error {
	unit := unitName(name)
	if !safeUnit.MatchString(unit) || strings.HasPrefix(unit, "-") {
		return fmt.Errorf("invalid service name %q", name)
	}
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("connect to the system bus: %w", err)
	}
	defer conn.Close()
	var job dbus.ObjectPath
	call := conn.Object(systemdBus, systemdPath).CallWithContext(ctx, systemdBus+".Manager.RestartUnit", 0, unit, "replace")
	if err := call.Store(&job); err != nil {
		var de dbus.Error
		if errors.As(err, &de) && (de.Name == "org.freedesktop.DBus.Error.AccessDenied" ||
			de.Name == "org.freedesktop.DBus.Error.InteractiveAuthorizationRequired") {
			return fmt.Errorf("%w: %s. Run sudo pulse install, which grants it for monitors with heal: restart", host.ErrHealNotAllowed, name)
		}
		return fmt.Errorf("restart %s: %w", name, err)
	}
	// The job object exists until the restart is done (or has failed).
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := conn.Object(systemdBus, job).GetProperty(systemdBus + ".Job.State"); err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("restart %s: still running when the time ran out", name)
		case <-tick.C:
		}
	}
}

// SetHealRights writes, replaces or removes pulse's polkit rule.
func (Platform) SetHealRights(_ context.Context, services []string) (host.HealRights, error) {
	res := host.HealRights{Steps: []string{}, Warnings: []string{}}
	if os.Geteuid() != 0 {
		return res, fmt.Errorf("heal rights: %w", host.ErrNotAdmin)
	}
	units, skipped := healUnits(services)
	for _, s := range skipped {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%q is not a service name pulse can allow; heal: restart will not work for it", s))
	}
	if len(units) == 0 {
		if err := os.Remove(polkitRule); err == nil {
			res.Steps = append(res.Steps, "Took away pulse's right to restart services: no monitor heals by restarting any more")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
		return res, nil
	}
	if fi, err := os.Stat(polkitRulesDir); err != nil || !fi.IsDir() {
		// polkit before 0.106 has no rules.d and cannot limit a right to single units.
		res.Warnings = append(res.Warnings, "heal: restart cannot work on this server: its polkit cannot limit a right to single services "+
			"(it needs polkit 0.106 or newer, as in Ubuntu 24.04, Debian 12 and RHEL 9). Use heal: run with a program instead")
		return res, nil
	}
	want := polkitRuleText(units)
	if have, err := os.ReadFile(polkitRule); err == nil && bytes.Equal(have, want) {
		return res, nil
	}
	if err := fsutil.WriteAtomic(polkitRule, want, 0o644); err != nil {
		return res, fmt.Errorf("write %s: %w", polkitRule, err)
	}
	res.Steps = append(res.Steps, "Let the service restart only these services, for self-healing: "+strings.Join(units, ", "))
	return res, nil
}

// healUnits turns service names into sorted, unique unit names and lists those it refuses.
func healUnits(services []string) (units, skipped []string) {
	seen := map[string]bool{}
	for _, s := range services {
		u := unitName(s)
		if !safeUnit.MatchString(u) || strings.HasPrefix(u, "-") {
			skipped = append(skipped, s)
			continue
		}
		if !seen[u] {
			seen[u] = true
			units = append(units, u)
		}
	}
	sort.Strings(units)
	return units, skipped
}

// polkitRuleText allows the pulse service account to restart only units, nothing else.
func polkitRuleText(units []string) []byte {
	list, _ := json.Marshal(units) // a JSON array of plain strings is a valid JavaScript array
	return []byte(fmt.Sprintf(`// Written by pulse install. Do not edit: pulse rewrites it from the monitors with heal: restart.
// Lets the %[1]s service restart these units, and nothing else, for self-healing.
polkit.addRule(function(action, subject) {
    if (action.id == "org.freedesktop.systemd1.manage-units" && subject.user == "%[1]s") {
        var units = %[2]s;
        if (action.lookup("verb") == "restart" && units.indexOf(action.lookup("unit")) >= 0) {
            return polkit.Result.YES;
        }
    }
});
`, serviceUser, list))
}
