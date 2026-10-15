// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/duration"
	"github.com/Aadilkaifi/brynko-pulse/internal/silence"
)

const maxReason = 200

func cmdSilence(args []string, stdout, stderr io.Writer) int {
	p := platformFor()
	fs := newFlags("silence", stderr)
	cfgDir := fs.String("config", p.ConfigDir(), "configuration directory")
	dataDir := fs.String("data", p.DataDir(), "where state and alert history are kept")
	reason := fs.String("reason", "", "why, shown in pulse status and the history")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage:
  pulse silence <monitor-id|all> <duration> [--reason text]   pause alerts, for example: pulse silence web 1h --reason deploy
  pulse silence list                                          show silences in force
  pulse silence clear <monitor-id|all>                        end a silence ("all" ends every one)

Monitors keep being checked while silenced. If one is still failing when the
silence ends, you get one alert.
`)
		fs.PrintDefaults()
	}
	// Flags may follow the positional arguments, as in the usage line.
	if fs.Parse(reorderFlags(args)) != nil {
		return exitUsage
	}
	a := fs.Args()
	now := time.Now()
	switch {
	case len(a) == 1 && a[0] == "list":
		list, err := silence.Load(*dataDir, now)
		return printSilences(stdout, stderr, *asJSON, list, err)
	case len(a) == 2 && a[0] == "clear":
		if !idRe.MatchString(a[1]) {
			fs.Usage()
			return exitUsage
		}
		n, err := silence.Clear(*dataDir, a[1], now)
		if *asJSON {
			return finishJSON(stdout, stderr, struct {
				envelope
				Cleared int `json:"cleared"`
			}{newEnvelope("silence", err), n}, err)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitInvalid
		}
		if n == 0 {
			fmt.Fprintf(stdout, "%s was not silenced. Nothing was changed.\n", a[1])
		} else {
			fmt.Fprintf(stdout, "Ended %d silence(s). Alerts are sent again.\n", n)
		}
		return exitOK
	case len(a) == 2:
		s, err := addSilence(*cfgDir, *dataDir, a[0], a[1], *reason, now)
		if *asJSON {
			return finishJSON(stdout, stderr, struct {
				envelope
				Silence *silence.Silence `json:"silence,omitempty"`
			}{newEnvelope("silence", err), s}, err)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitInvalid
		}
		target := "Monitor " + s.Monitor + " is"
		if s.Monitor == silence.All {
			target = "Every monitor is"
		}
		fmt.Fprintf(stdout, "%s silenced until %s (%s).\nEnd it early with: pulse silence clear %s\n",
			target, s.Until.Local().Format("2006-01-02 15:04"), alert.Human(s.Until.Sub(now)), s.Monitor)
		return exitOK
	}
	fs.Usage()
	return exitUsage
}

func addSilence(cfgDir, dataDir, target, length, reason string, now time.Time) (*silence.Silence, error) {
	if !idRe.MatchString(target) {
		return nil, fmt.Errorf("%q is not a monitor id", target)
	}
	d, err := duration.Parse(length)
	if err != nil {
		return nil, fmt.Errorf("%q is not a duration; use for example 30m, 2h or 1d", length)
	}
	if err := silence.CheckDuration(d); err != nil {
		return nil, err
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > maxReason {
		return nil, fmt.Errorf("the reason is longer than %d characters", maxReason)
	}
	if target != silence.All {
		// Only the ids are needed, so an unset secret does not block a silence.
		cfg, res := config.Load(cfgDir)
		if !res.OK() {
			return nil, fmt.Errorf("cannot read the configuration to check the monitor id (run pulse validate)")
		}
		found := false
		for _, m := range cfg.Monitors {
			found = found || m.ID == target
		}
		if !found {
			return nil, fmt.Errorf("there is no monitor with id %s. See the ids with: pulse status", target)
		}
	}
	s, err := silence.Add(dataDir, target, d, reason, now)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func printSilences(stdout, stderr io.Writer, asJSON bool, list []silence.Silence, err error) int {
	if asJSON {
		if list == nil {
			list = []silence.Silence{}
		}
		return finishJSON(stdout, stderr, struct {
			envelope
			Silences []silence.Silence `json:"silences"`
		}{newEnvelope("silence", err), list}, err)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "Nothing is silenced.")
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MONITOR\tUNTIL\tLEFT\tREASON")
	for _, s := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Monitor, s.Until.Local().Format("2006-01-02 15:04"),
			alert.Human(time.Until(s.Until)), oneLine(s.Reason))
	}
	tw.Flush()
	return exitOK
}

// reorderFlags moves flags in front of positional arguments, because Go's flag
// package stops at the first positional one. Everything after "--" stays as is.
func reorderFlags(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return append(append(flags, a), append(rest, args[i+1:]...)...)
		case strings.HasPrefix(a, "-") && len(a) > 1:
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && takesValue(a) {
				i++
				flags = append(flags, args[i])
			}
		default:
			rest = append(rest, a)
		}
	}
	return append(flags, rest...)
}

// takesValue reports whether a flag is followed by a separate value. Boolean
// flags are the exception, so they are listed here.
func takesValue(flag string) bool {
	switch strings.TrimLeft(flag, "-") {
	case "json", "dry-run", "yes", "purge", "allow-docker", "h", "help":
		return false
	}
	return true
}
