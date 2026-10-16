// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/Aadilkaifi/brynko-pulse/internal/fsutil"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// The service runs as its own virtual account, which has no password and only
// the rights granted to it below.
const serviceAccount = `NT SERVICE\` + host.ServiceName

const eventSources = eventlog.Error | eventlog.Warning | eventlog.Info

func binaryPath() string {
	dir := os.Getenv("ProgramFiles")
	if dir == "" {
		dir = `C:\Program Files`
	}
	return filepath.Join(dir, host.ServiceName, "pulse.exe")
}

// Install copies the binary, registers the Windows Service under a virtual
// account, gives that account read access to the config and full access to its
// data folder, and starts it. Running it again updates everything in place.
func (p Platform) Install(ctx context.Context, o host.InstallOptions) (host.InstallResult, error) {
	bin := binaryPath()
	res := host.InstallResult{Binary: bin, Service: host.ServiceName, Account: serviceAccount,
		Steps: []string{}, Warnings: []string{},
		Next: []string{
			"See what it is watching: pulse status",
			"Its log is in Event Viewer: Windows Logs, Application, source " + host.ServiceName,
			"After changing the configuration: sc.exe control " + host.ServiceName + " paramchange",
		}}
	step := func(s string) { res.Steps = append(res.Steps, s) }
	if !windows.GetCurrentProcessToken().IsElevated() {
		return res, fmt.Errorf("install: %w. Open the terminal with Run as administrator", host.ErrNotAdmin)
	}

	m, err := mgr.Connect()
	if err != nil {
		return res, fmt.Errorf("connect to the service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(host.ServiceName)
	exists := err == nil
	if exists {
		defer s.Close()
		if stopped, err := stopService(ctx, s); err != nil {
			return res, err
		} else if stopped {
			step("Stopped the running service to update it")
		}
	}

	copied, err := fsutil.CopyAtomic(o.Source, bin, 0o755)
	if err != nil {
		return res, fmt.Errorf("copy pulse to %s: %w", bin, err)
	}
	if copied {
		step("Copied pulse to " + bin)
	}

	cfg := mgr.Config{
		ServiceType:      windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:        mgr.StartAutomatic,
		ErrorControl:     mgr.ErrorNormal,
		DisplayName:      host.ServiceName,
		Description:      "Watches this server and sends alerts when something breaks.",
		ServiceStartName: serviceAccount,
		SidType:          windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}
	args := []string{"run", "--config", o.ConfigDir, "--data", o.DataDir}
	if exists {
		cfg.BinaryPathName = commandLine(bin, args)
		if err := s.UpdateConfig(cfg); err != nil {
			return res, fmt.Errorf("update the service: %w", err)
		}
		step("Updated the " + host.ServiceName + " service")
	} else {
		if s, err = m.CreateService(host.ServiceName, bin, cfg, args...); err != nil {
			return res, fmt.Errorf("create the service: %w", err)
		}
		defer s.Close()
		step("Registered the " + host.ServiceName + " service, running as " + serviceAccount)
	}
	if err := setRestartOnFailure(s); err != nil {
		return res, err
	}
	if err := eventlog.InstallAsEventCreate(host.ServiceName, eventSources); err != nil && !strings.Contains(err.Error(), "already exists") {
		return res, fmt.Errorf("register the event log source: %w", err)
	}

	if err := grantAccess(o.ConfigDir, o.DataDir); err != nil {
		return res, err
	}
	step("Let the service read " + o.ConfigDir + " and write " + o.DataDir)

	if err := s.Start(); err != nil {
		return res, fmt.Errorf("start the service: %w", err)
	}
	if err := waitState(ctx, s, svc.Running, 15*time.Second); err != nil {
		return res, fmt.Errorf("the service did not start (%v). See why in Event Viewer, Windows Logs, Application, source %s", err, host.ServiceName)
	}
	step("Started " + host.ServiceName + "; it starts again after a reboot")
	return res, nil
}

// Uninstall stops and deletes the service, its event log source and the binary.
// Config and data stay. The virtual account disappears with the service.
func (Platform) Uninstall(ctx context.Context, _ host.UninstallOptions) (host.UninstallResult, error) {
	res := host.UninstallResult{Steps: []string{}}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return res, fmt.Errorf("uninstall: %w. Open the terminal with Run as administrator", host.ErrNotAdmin)
	}
	m, err := mgr.Connect()
	if err != nil {
		return res, fmt.Errorf("connect to the service manager: %w", err)
	}
	defer m.Disconnect()
	if s, err := m.OpenService(host.ServiceName); err == nil {
		_, stopErr := stopService(ctx, s)
		delErr := s.Delete()
		s.Close()
		if err := errors.Join(stopErr, delErr); err != nil {
			return res, fmt.Errorf("remove the service: %w", err)
		}
		res.Steps = append(res.Steps, "Stopped and removed the "+host.ServiceName+" service")
	}
	_ = eventlog.Remove(host.ServiceName)

	bin := binaryPath()
	later, err := removeBinary(bin)
	switch {
	case err == nil && later:
		res.Steps = append(res.Steps, "Removed "+bin+" (Windows deletes the file at the next restart, as it is in use now)")
	case err == nil:
		res.Steps = append(res.Steps, "Removed "+bin)
		_ = os.Remove(filepath.Dir(bin)) // only succeeds when empty
	case !errors.Is(err, os.ErrNotExist):
		return res, err
	}
	return res, nil
}

// removeBinary deletes pulse.exe. Windows keeps the file of a running program, and `pulse uninstall` is
// usually run from that very file: then it is renamed aside (allowed while it runs) and Windows is asked
// to delete it at the next restart. `later` says so.
func removeBinary(bin string) (later bool, err error) {
	err = os.Remove(bin)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	self, serr := os.Executable()
	if serr != nil || !strings.EqualFold(filepath.Clean(self), filepath.Clean(bin)) {
		return false, err
	}
	old := bin + ".old"
	_ = os.Remove(old)
	if err := os.Rename(bin, old); err != nil {
		return false, err
	}
	p, err := windows.UTF16PtrFromString(old)
	if err != nil {
		return false, err
	}
	return true, windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}

func commandLine(bin string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	for _, a := range append([]string{bin}, args...) {
		parts = append(parts, windows.EscapeArg(a))
	}
	return strings.Join(parts, " ")
}

func setRestartOnFailure(s *mgr.Service) error {
	err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, uint32((24 * time.Hour).Seconds()))
	if err == nil {
		// Also restart when pulse exits with an error, not only when it crashes.
		err = s.SetRecoveryActionsOnNonCrashFailures(true)
	}
	if err != nil {
		return fmt.Errorf("set the service to restart on failure: %w", err)
	}
	return nil
}

func grantAccess(configDir, dataDir string) error {
	sid, _, _, err := windows.LookupSID("", serviceAccount)
	if err != nil {
		return fmt.Errorf("look up %s: %w", serviceAccount, err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "heartbeats"), 0o750); err != nil {
		return err
	}
	if err := setTreeACL(configDir, adminOnlySDDL+fmt.Sprintf(serviceReadACE, sid), dataDir); err != nil {
		return err
	}
	return setTreeACL(dataDir, adminOnlySDDL+fmt.Sprintf(serviceFullACE, sid), "")
}

// stopService stops s if it is running and reports whether it had to.
func stopService(ctx context.Context, s *mgr.Service) (bool, error) {
	st, err := s.Query()
	if err != nil {
		return false, fmt.Errorf("query the service: %w", err)
	}
	if st.State == svc.Stopped {
		return false, nil
	}
	if st.State != svc.StopPending {
		if _, err := s.Control(svc.Stop); err != nil {
			return false, fmt.Errorf("stop the service: %w", err)
		}
	}
	return true, waitState(ctx, s, svc.Stopped, 30*time.Second)
}

func waitState(ctx context.Context, s *mgr.Service, want svc.State, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		if want == svc.Running && st.State == svc.Stopped {
			return errors.New("it stopped right after starting")
		}
		select {
		case <-ctx.Done():
			return errors.New("timed out")
		case <-tick.C:
		}
	}
}

// ReloadService asks the running service to reread its configuration.
func (Platform) ReloadService(context.Context) (bool, error) {
	m, err := mgr.Connect()
	if err != nil {
		return false, fmt.Errorf("connect to the service manager: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(host.ServiceName)
	if err != nil {
		return false, nil
	}
	defer s.Close()
	if st, err := s.Query(); err != nil || st.State != svc.Running {
		return false, nil
	}
	if _, err := s.Control(svc.ParamChange); err != nil {
		return false, fmt.Errorf("reload the service: %w", err)
	}
	return true, nil
}

// RunAsService runs fn under the Service Control Manager when Windows started
// pulse as a service. It reports false when pulse was started from a terminal.
func (Platform) RunAsService(fn host.ServiceFunc) (bool, error) {
	is, err := svc.IsWindowsService()
	if err != nil || !is {
		return false, err
	}
	el, err := eventlog.Open(host.ServiceName)
	if err != nil {
		return true, fmt.Errorf("open the event log: %w", err)
	}
	defer el.Close()
	return true, svc.Run(host.ServiceName, &handler{fn: fn, log: slog.New(newEventLogHandler(el))})
}

type handler struct {
	fn  host.ServiceFunc
	log *slog.Logger
}

// Execute follows the Service Control Manager protocol. Stop and shutdown
// cancel pulse; "sc control brynko-pulse paramchange" reloads the config.
func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptParamChange
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reload := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- h.fn(ctx, reload, h.log) }()
	status <- svc.Status{State: svc.Running, Accepts: accepts}

	for {
		select {
		case err := <-done:
			if err != nil {
				h.log.Error("pulse stopped", "error", err.Error())
				return true, 1 // non-zero so the restart-on-failure actions apply
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.ParamChange:
				select {
				case reload <- struct{}{}:
				default: // a reload is already queued
				}
				status <- svc.Status{State: svc.Running, Accepts: accepts}
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			}
		}
	}
}

// eventLogHandler writes slog records to the Windows Event Log, by level.
type eventLogHandler struct {
	mu    *sync.Mutex
	buf   *bytes.Buffer
	inner slog.Handler
	el    *eventlog.Log
}

func newEventLogHandler(el *eventlog.Log) *eventLogHandler {
	buf := &bytes.Buffer{}
	inner := slog.NewTextHandler(buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{} // the Event Log records the time itself
			}
			return a
		},
	})
	return &eventLogHandler{mu: &sync.Mutex{}, buf: buf, inner: inner, el: el}
}

func (h *eventLogHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *eventLogHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf.Reset()
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}
	msg := strings.TrimSpace(h.buf.String())
	switch {
	case r.Level >= slog.LevelError:
		return h.el.Error(1, msg)
	case r.Level >= slog.LevelWarn:
		return h.el.Warning(1, msg)
	default:
		return h.el.Info(1, msg)
	}
}

func (h *eventLogHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.inner = h.inner.WithAttrs(as)
	return &c
}

func (h *eventLogHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.inner = h.inner.WithGroup(name)
	return &c
}
