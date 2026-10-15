// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/channel"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/fsutil"
	"github.com/Aadilkaifi/brynko-pulse/internal/monitor"
	"github.com/Aadilkaifi/brynko-pulse/internal/notify"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
	"github.com/Aadilkaifi/brynko-pulse/internal/runner"
)

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func cmdTest(args []string, stdout, stderr io.Writer) int {
	p := platform.Current()
	fs := newFlags("test", stderr)
	cfgDir := fs.String("config", p.ConfigDir(), "configuration directory")
	dataDir := fs.String("data", p.DataDir(), "where state and alert history are kept")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage:\n  pulse test channel <name>   send a test alert\n  pulse test monitor <id>     run one monitor once and show what it measures")
		fs.PrintDefaults()
	}
	if fs.Parse(args) != nil {
		return exitUsage
	}
	if fs.NArg() != 2 || (fs.Arg(0) != "channel" && fs.Arg(0) != "monitor") {
		fs.Usage()
		return exitUsage
	}
	cfg, err := runner.Load(runner.Options{ConfigDir: *cfgDir, Platform: p})
	if err != nil {
		return fail(stdout, stderr, *asJSON, "test", err)
	}
	if fs.Arg(0) == "channel" {
		return testChannel(cfg, fs.Arg(1), stdout, stderr, *asJSON)
	}
	return testMonitor(cfg, fs.Arg(1), *dataDir, p, stdout, stderr, *asJSON)
}

func fail(stdout, stderr io.Writer, asJSON bool, command string, err error) int {
	if asJSON {
		return finishJSON(stdout, stderr, newEnvelope(command, err), err)
	}
	fmt.Fprintln(stderr, err)
	return exitInvalid
}

func testChannel(cfg *config.Config, id string, stdout, stderr io.Writer, asJSON bool) int {
	d := notify.New(cfg, channel.NewHTTPClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	a := alert.Alert{Server: cfg.ServerName, Monitor: "test", MonitorName: "test", MonitorType: "test",
		Kind: alert.KindTest, Severity: "info", At: time.Now(),
		Detail: fmt.Sprintf("This is a test alert from pulse on %s. If you can read it, the %s channel works.", cfg.ServerName, id)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := d.SendNow(ctx, id, a)
	if err != nil {
		err = fmt.Errorf("the test alert through %s failed: %w", id, err)
	}
	if asJSON {
		return finishJSON(stdout, stderr, struct {
			envelope
			Channel string `json:"channel"`
		}{newEnvelope("test", err), id}, err)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	fmt.Fprintf(stdout, "Sent a test alert through %s. Check that it arrived.\n", id)
	return exitOK
}

func testMonitor(cfg *config.Config, id, dataDir string, p platform.Platform, stdout, stderr io.Writer, asJSON bool) int {
	var m *config.Monitor
	for i := range cfg.Monitors {
		if cfg.Monitors[i].ID == id {
			eff := cfg.Monitors[i].Effective(cfg.Defaults)
			m = &eff
		}
	}
	if m == nil {
		return fail(stdout, stderr, asJSON, "test", fmt.Errorf("there is no monitor with id %s", id))
	}
	chk, defaultAlert, err := monitor.New(*m, monitor.Deps{Platform: p, DataDir: dataDir,
		OutputLimit: cfg.Limits.CommandOutputBytes, Started: time.Now()})
	if err != nil {
		return fail(stdout, stderr, asJSON, "test", err)
	}
	cond := m.AlertWhen
	if cond == "" {
		cond = defaultAlert
	}
	alertC, err := rule.Parse(cond)
	if err != nil {
		return fail(stdout, stderr, asJSON, "test", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	values, err := chk.Check(ctx)
	if errors.Is(err, monitor.ErrNotReady) { // measured between two samples, such as cpu
		time.Sleep(time.Second)
		values, err = chk.Check(ctx)
	}
	var wouldAlert bool
	var detail string
	if errors.Is(err, monitor.ErrNotReady) {
		detail = "this monitor follows new events while pulse runs, so a one-off test has nothing to measure"
	} else if err != nil {
		wouldAlert, detail = true, "check failed: "+err.Error()
	} else if wouldAlert, err = alertC.Eval(values); err != nil {
		wouldAlert, detail = true, err.Error()
	} else {
		detail = alertC.Describe(values)
	}
	if asJSON {
		return writeJSON(stdout, stderr, struct {
			envelope
			Monitor    string      `json:"monitor"`
			Values     rule.Values `json:"values"`
			Condition  string      `json:"condition"`
			WouldAlert bool        `json:"would_alert"`
			Detail     string      `json:"detail"`
		}{newEnvelope("test", nil), id, values, alertC.Raw, wouldAlert, detail})
	}
	for _, k := range rule.Names(values) {
		fmt.Fprintf(stdout, "  %-22s %s\n", k, rule.Format(values[k]))
	}
	verdict := "not met, no alert"
	if wouldAlert {
		verdict = "MET, this would alert"
	}
	fmt.Fprintf(stdout, "Alert rule: %s  ->  %s (%s)\n", alertC.Raw, verdict, detail)
	return exitOK
}

func cmdPing(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("ping", stderr)
	dataDir := fs.String("data", platform.Current().DataDir(), "where state and alert history are kept")
	if fs.Parse(args) != nil {
		return exitUsage
	}
	if fs.NArg() != 1 || !idRe.MatchString(fs.Arg(0)) {
		fmt.Fprintln(stderr, "Usage: pulse ping <heartbeat-monitor-id>")
		return exitUsage
	}
	path := monitor.HeartbeatFile(*dataDir, fs.Arg(0))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		fmt.Fprintln(stderr, "Cannot record the heartbeat:", err)
		return exitInvalid
	}
	// Replacing the file, rather than touching it, works for any user allowed to
	// write in the folder, whoever pinged last.
	if err := fsutil.WriteAtomic(path, nil, 0o660); err != nil {
		fmt.Fprintln(stderr, "Cannot record the heartbeat:", err)
		return exitInvalid
	}
	fmt.Fprintf(stdout, "Heartbeat recorded for %s.\n", fs.Arg(0))
	return exitOK
}
