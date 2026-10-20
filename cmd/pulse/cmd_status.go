// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/engine"
	"github.com/Aadilkaifi/brynko-pulse/internal/store"
)

// staleAfter is how old state.json may be before status warns that pulse may be stopped.
const staleAfter = time.Minute

func cmdStatus(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("status", stderr)
	dataDir := fs.String("data", platformFor().DataDir(), "where state and alert history are kept")
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(reorderFlags(args)) != nil {
		return exitUsage
	}

	snap, err := store.ReadState(*dataDir)
	if err == nil && snap == nil {
		err = fmt.Errorf("pulse has not reported yet (no state in %s). If its service was just started, wait a few seconds; otherwise start it: %s", *dataDir, platformFor().StartCommand())
	}
	stale := err == nil && time.Since(snap.Updated) > staleAfter
	if *asJSON {
		out := struct {
			envelope
			Stale bool `json:"stale"`
			*store.Snapshot
		}{newEnvelope("status", err), stale, snap}
		return finishJSON(stdout, stderr, out, err)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}

	fmt.Fprintf(stdout, "%s  (pulse %s, updated %s ago)\n", snap.Server, snap.Version, alert.Human(time.Since(snap.Updated)))
	if stale {
		fmt.Fprintln(stdout, "Warning: this state is old, so pulse may not be running. Check with your service manager.")
	}
	fmt.Fprintln(stdout)

	ids := make([]string, 0, len(snap.Monitors))
	for id := range snap.Monitors {
		ids = append(ids, id)
	}
	rank := map[engine.Status]int{engine.Firing: 0, engine.Pending: 1, engine.Unknown: 2, engine.OK: 3}
	sort.Slice(ids, func(i, j int) bool {
		a, b := snap.Monitors[ids[i]], snap.Monitors[ids[j]]
		if a.Enabled != b.Enabled {
			return a.Enabled // inactive monitors last
		}
		if ra, rb := rank[a.Status], rank[b.Status]; ra != rb {
			return ra < rb
		}
		return ids[i] < ids[j]
	})

	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MONITOR\tSTATUS\tFOR\tDETAIL")
	for _, id := range ids {
		m := snap.Monitors[id]
		status, since, detail := string(m.Status), "", m.Detail
		if !m.Enabled {
			status, detail = "inactive", "turned off (enabled: false)"
		} else if m.Error != "" {
			status, detail = "not running", m.Error
		}
		if !m.ProblemSince.IsZero() {
			since = alert.Human(time.Since(m.ProblemSince))
		}
		if m.Flapping {
			status += ", flapping"
		}
		if !m.SilencedUntil.IsZero() && time.Now().Before(m.SilencedUntil) {
			status += ", silenced"
			detail = fmt.Sprintf("silenced until %s. %s", m.SilencedUntil.Local().Format("2006-01-02 15:04"), detail)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", id, strings.ToUpper(status), since, oneLine(detail))
	}
	tw.Flush()

	if len(snap.Channels) > 0 {
		fmt.Fprintln(stdout)
		tw = tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "CHANNEL\tTYPE\tSTATE")
		names := make([]string, 0, len(snap.Channels))
		for n := range snap.Channels {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", n, snap.Channels[n].Type, channelState(snap.Channels[n]))
		}
		tw.Flush()
	}
	if w := snap.Watchdog; w != nil {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Dead-server alert: "+watchdogState(*w))
	}
	return exitOK
}

func watchdogState(w store.WatchdogStatus) string {
	to := "nobody (no email channel)"
	if len(w.Email) > 0 {
		to = strings.Join(w.Email, ", ")
	}
	switch {
	case w.LastError != "" && w.LastErrorAt.After(w.LastOK):
		return fmt.Sprintf("last check-in failed %s ago: %s", alert.Human(time.Since(w.LastErrorAt)), oneLine(w.LastError))
	case !w.LastOK.IsZero():
		return fmt.Sprintf("on, checked in %s ago; alerts %s", alert.Human(time.Since(w.LastOK)), to)
	default:
		return "on, not checked in yet; alerts " + to
	}
}

func channelState(c store.ChannelStatus) string {
	switch {
	case c.Unavailable != "":
		return "cannot be used: " + c.Unavailable
	case c.LastError != "" && c.LastErrorAt.After(c.LastSent):
		return fmt.Sprintf("last delivery failed %s ago: %s", alert.Human(time.Since(c.LastErrorAt)), oneLine(c.LastError))
	case !c.LastSent.IsZero():
		return fmt.Sprintf("ok, last alert sent %s ago", alert.Human(time.Since(c.LastSent)))
	default:
		return "ok, nothing sent yet"
	}
}

func cmdHistory(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("history", stderr)
	dataDir := fs.String("data", platformFor().DataDir(), "where state and alert history are kept")
	limit := fs.Int("limit", 20, "how many alerts to show, newest first")
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(reorderFlags(args)) != nil {
		return exitUsage
	}
	var err error
	if *limit < 1 {
		err = errors.New("--limit must be at least 1")
	}
	var recs []store.Record
	if err == nil {
		recs, err = store.History(*dataDir, *limit)
	}
	if *asJSON {
		if recs == nil {
			recs = []store.Record{}
		}
		return finishJSON(stdout, stderr, struct {
			envelope
			Alerts []store.Record `json:"alerts"`
		}{newEnvelope("history", err), recs}, err)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	if len(recs) == 0 {
		fmt.Fprintln(stdout, "No alerts yet.")
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tKIND\tSEVERITY\tMONITOR\tDETAIL")
	for _, r := range recs {
		kind := strings.ToUpper(r.Kind)
		if r.Silenced {
			kind += " (silenced)"
		}
		detail := r.Detail
		if r.Kind == store.KindUndelivered {
			detail = "not delivered to " + strings.Join(r.Channels, ", ") + ": " + detail
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.At.Local().Format("2006-01-02 15:04"),
			kind, r.Severity, r.Monitor, oneLine(detail))
	}
	tw.Flush()
	return exitOK
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 100 {
		s = s[:97] + "..."
	}
	return s
}
