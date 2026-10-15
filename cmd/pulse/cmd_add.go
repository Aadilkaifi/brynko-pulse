// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/quickadd"
)

func cmdAdd(args []string, stdout, stderr io.Writer) int {
	p := platformFor()
	fs := newFlags("add", stderr)
	cfgDir := fs.String("config", p.ConfigDir(), "configuration directory")
	var s quickadd.Spec
	fs.StringVar(&s.ID, "id", "", "monitor id (default: made from the type and target)")
	fs.StringVar(&s.Name, "name", "", "name shown in alerts")
	notify := fs.String("notify", "", "channels, comma separated (default: the defaults in pulse.yaml)")
	fs.StringVar(&s.Severity, "severity", "", "info, warning or critical")
	fs.StringVar(&s.For, "for", "", "how long it must stay wrong before alerting, for example 2m")
	fs.StringVar(&s.Interval, "interval", "", "how often to check, for example 30s")
	above := fs.String("above", "", "alert above this percent (disk, memory, cpu, swap) or load per CPU")
	fs.StringVar(&s.When, "when", "", `full alert condition, for example "age > 1d"`)
	fs.StringVar(&s.ClearWhen, "clear-when", "", "when it counts as fixed again")
	fs.IntVar(&s.Days, "days", 0, "ssl: alert when fewer days are left")
	fs.IntVar(&s.Status, "status", 0, "http: the status code to expect")
	fs.StringVar(&s.Every, "every", "", "heartbeat: how often the job pings, for example 1d")
	fs.StringVar(&s.Grace, "grace", "", "heartbeat: extra time allowed before alerting")
	dryRun := fs.Bool("dry-run", false, "show the monitor and check it, without saving")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage: pulse add <type> [target] [flags]

Examples:
  pulse add disk /data --above 85
  pulse add memory
  pulse add service nginx --notify ops-email
  pulse add port 5432
  pulse add http https://example.com/health --status 200
  pulse add ssl example.com --days 21
  pulse add file /var/backups/db.tar.gz --when "age > 1d"
  pulse add heartbeat --id nightly-backup --every 1d
  pulse add command --id queue-ok -- /usr/local/bin/check-queue --max 100

Types: %s
The monitor is saved as monitors/<id>.yaml after the whole configuration is checked.

`, strings.Join(quickadd.Types, ", "))
		fs.PrintDefaults()
	}
	if fs.Parse(reorderFlags(args)) != nil {
		return exitUsage
	}
	pos := fs.Args()
	if dash := indexOf(args, "--"); dash >= 0 {
		s.Run = args[dash+1:]
		pos = pos[:len(pos)-len(s.Run)]
	}
	if len(pos) < 1 || len(pos) > 2 {
		fs.Usage()
		return exitUsage
	}
	s.Type = pos[0]
	if len(pos) == 2 {
		s.Target = pos[1]
	}
	if *notify != "" {
		for _, n := range strings.Split(*notify, ",") {
			if n = strings.TrimSpace(n); n != "" {
				s.Notify = append(s.Notify, n)
			}
		}
	}

	res, issues, reloaded, err := addMonitor(p.ReloadService, *cfgDir, s, *above, *dryRun)
	if *asJSON {
		return finishJSON(stdout, stderr, struct {
			envelope
			ID       string         `json:"id,omitempty"`
			File     string         `json:"file,omitempty"`
			YAML     string         `json:"yaml,omitempty"`
			Saved    bool           `json:"saved"`
			Reloaded bool           `json:"reloaded"`
			Issues   []config.Issue `json:"issues"`
		}{newEnvelope("add", err), res.ID, res.Path, string(res.YAML), err == nil && !*dryRun, reloaded, issues}, err)
	}
	if len(res.YAML) > 0 {
		fmt.Fprintf(stdout, "%s\n%s\n", res.Path, indent(string(res.YAML)))
	}
	for _, is := range issues {
		fmt.Fprintf(stdout, "error    %s\n", formatIssue(is))
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	switch {
	case *dryRun:
		fmt.Fprintln(stdout, "The configuration would be valid. Nothing was saved (--dry-run).")
	case reloaded:
		fmt.Fprintf(stdout, "Saved. The running service picked it up. Check it with: pulse test monitor %s\n", res.ID)
	default:
		fmt.Fprintf(stdout, "Saved. pulse is not running as a service here; it applies at the next start.\nCheck it now with: pulse test monitor %s\n", res.ID)
	}
	return exitOK
}

func addMonitor(reload func(context.Context) (bool, error), cfgDir string, s quickadd.Spec, above string, dryRun bool) (quickadd.Result, []config.Issue, bool, error) {
	if above != "" {
		f, err := strconv.ParseFloat(above, 64)
		if err != nil || f < 0 {
			return quickadd.Result{}, nil, false, fmt.Errorf("--above must be a number, got %q", above)
		}
		s.Above = &f
	}
	res, err := quickadd.Build(s)
	if err != nil {
		return res, nil, false, err
	}
	changes := []config.Change{{Path: res.Path, Data: res.YAML, CreateOnly: true}}
	var vr config.Result
	if dryRun {
		vr = config.Check(cfgDir, changes)
		if !vr.OK() {
			return res, vr.Errors, false, config.ErrInvalid
		}
		return res, []config.Issue{}, false, nil
	}
	vr, err = config.Apply(cfgDir, changes)
	if errors.Is(err, config.ErrInvalid) {
		for _, is := range vr.Errors {
			if strings.Contains(is.Message, fmt.Sprintf("monitor id %q is already used", res.ID)) {
				return res, nil, false, fmt.Errorf("a monitor with id %s already exists. Pick another id with --id", res.ID)
			}
		}
		return res, vr.Errors, false, err
	}
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			err = fmt.Errorf("a monitor file named %s already exists. Pick another id with --id", res.Path)
		}
		return res, nil, false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reloaded, err := reload(ctx)
	if err != nil {
		err = fmt.Errorf("saved %s, but the running service could not be reloaded: %w", res.Path, err)
	}
	return res, []config.Issue{}, reloaded, err
}

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return -1
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}
