// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// Rights the pulse service gets on a service it may restart: query status (LC),
// start (RP) and stop (WP). Nothing else, so it cannot reconfigure the service.
const healACE = "(A;;LCRPWP;;;%s)"

// openService opens one service with only the access asked for. mgr.Connect asks
// for full control of the service manager, which only administrators have: the
// pulse service, under its own account, must ask for less.
func openService(name string, access uint32) (*mgr.Service, func(), error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to the service manager: %w", err)
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		windows.CloseServiceHandle(scm)
		return nil, nil, err
	}
	h, err := windows.OpenService(scm, p, access)
	if err != nil {
		windows.CloseServiceHandle(scm)
		switch {
		case errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST):
			return nil, nil, fmt.Errorf("service %s: %w", name, host.ErrNotFound)
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return nil, nil, fmt.Errorf("service %s: %w", name, windows.ERROR_ACCESS_DENIED)
		}
		return nil, nil, fmt.Errorf("open service %s: %w", name, err)
	}
	return &mgr.Service{Name: name, Handle: h}, func() {
		windows.CloseServiceHandle(h)
		windows.CloseServiceHandle(scm)
	}, nil
}

// RestartService stops a service, starts it again and waits until it runs.
func (Platform) RestartService(ctx context.Context, name string) error {
	s, done, err := openService(name, windows.SERVICE_STOP|windows.SERVICE_START|windows.SERVICE_QUERY_STATUS)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("%w: %s. Run pulse install as Administrator, which grants it for monitors with heal: restart", host.ErrHealNotAllowed, name)
	}
	if err != nil {
		return err
	}
	defer done()
	if _, err := stopService(ctx, s); err != nil {
		return fmt.Errorf("restart %s: %w", name, err)
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("start %s again: %w", name, err)
	}
	if err := waitState(ctx, s, svc.Running, 60*time.Second); err != nil {
		return fmt.Errorf("start %s again: %w", name, err)
	}
	return nil
}

// The services pulse was allowed to restart are recorded under its own service key,
// which only administrators may change, so a later change only touches those.
const healRecordKey = `SYSTEM\CurrentControlSet\Services\` + host.ServiceName + `\Parameters`

// SetHealRights gives the pulse service account start and stop rights on exactly
// these services, and takes them away from the services it had them on before.
func (Platform) SetHealRights(_ context.Context, services []string) (host.HealRights, error) {
	res := host.HealRights{Steps: []string{}, Warnings: []string{}}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return res, fmt.Errorf("heal rights: %w", host.ErrNotAdmin)
	}
	before := readHealRecord()
	if len(services) == 0 && len(before) == 0 {
		return res, nil
	}
	sid, _, _, err := windows.LookupSID("", serviceAccount)
	if err != nil {
		return res, fmt.Errorf("the pulse service is not installed, so it cannot be allowed to restart services: %w", err)
	}
	want := map[string]bool{}
	for _, s := range services {
		want[strings.ToLower(s)] = true
	}
	touch := map[string]string{} // lower-case name -> name
	for _, s := range append(append([]string{}, before...), services...) {
		touch[strings.ToLower(s)] = s
	}
	var granted, removed, kept []string
	for key, name := range touch {
		changed, err := setServiceACE(name, sid.String(), want[key])
		switch {
		case err != nil && want[key]:
			res.Warnings = append(res.Warnings, fmt.Sprintf("could not let pulse restart %s: %v", name, err))
			continue
		case err != nil:
			continue // a service that was removed since: nothing to take away
		case want[key]:
			kept = append(kept, name)
			if changed {
				granted = append(granted, name)
			}
		case changed:
			removed = append(removed, name)
		}
	}
	sort.Strings(kept)
	if err := writeHealRecord(kept); err != nil {
		res.Warnings = append(res.Warnings, "could not record which services pulse may restart: "+err.Error())
	}
	sort.Strings(granted)
	sort.Strings(removed)
	if len(granted) > 0 {
		res.Steps = append(res.Steps, "Let the service restart these services, for self-healing: "+strings.Join(granted, ", "))
	}
	if len(removed) > 0 {
		res.Steps = append(res.Steps, "Took away the right to restart: "+strings.Join(removed, ", "))
	}
	return res, nil
}

func readHealRecord() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, healRecordKey, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	list, _, err := k.GetStringsValue("HealServices")
	if err != nil {
		return nil
	}
	return list
}

func writeHealRecord(list []string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, healRecordKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if len(list) == 0 {
		if err := k.DeleteValue("HealServices"); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	return k.SetStringsValue("HealServices", list)
}

// setServiceACE adds or removes pulse's entry in one service's permissions and
// reports whether it changed anything.
func setServiceACE(name, sid string, want bool) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(name, windows.SE_SERVICE, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	before := sd.String()
	after := withHealACE(before, sid, want)
	if after == before {
		return false, nil
	}
	next, err := windows.SecurityDescriptorFromString(after)
	if err != nil {
		return false, err
	}
	dacl, _, err := next.DACL()
	if err != nil {
		return false, err
	}
	return true, windows.SetNamedSecurityInfo(name, windows.SE_SERVICE, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// withHealACE returns a service's SDDL with exactly one pulse entry when want is
// true and none when it is false. Every other entry stays as it was.
func withHealACE(sddl, sid string, want bool) string {
	ours := regexp.MustCompile(`\(A;[^;()]*;[^;()]*;;;` + regexp.QuoteMeta(sid) + `\)`)
	clean := ours.ReplaceAllString(sddl, "")
	if !want {
		return clean
	}
	ace := fmt.Sprintf(healACE, sid)
	// The DACL is "D:" plus flags and entries; SACL and the rest, if present, follow "S:".
	if i := strings.Index(clean, "S:"); i >= 0 {
		return clean[:i] + ace + clean[i:]
	}
	return clean + ace
}
