// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/monitor"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// Target is one thing on this server a monitor can watch.
type Target struct {
	Value string `json:"value"`          // what goes into the monitor's target field
	Note  string `json:"note,omitempty"` // a few plain words about it
}

const suggestDetails = `Lists what this server has, so a form can offer it instead of a text box:
  disk        filesystems that hold data, with their size and use
  service     running services
  container   running Docker containers
  port        TCP ports something listens on

Unlike pulse discover, it proposes no monitors and lists things already watched too.`

func cmdSuggest(args []string, stdout, stderr io.Writer) int {
	p := platformFor()
	fs := newFlags("suggest", stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(reorderFlags(args)) != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	notes := []string{}
	inv, err := p.Inventory(ctx)
	if err != nil {
		notes = append(notes, "services, ports and filesystems were skipped: "+err.Error())
	}
	containers, cerr := monitor.ListContainers(ctx)
	if cerr != nil {
		notes = append(notes, "containers were skipped: "+cerr.Error())
	}
	targets := suggestTargets(inv, containers)

	if *asJSON {
		return finishJSON(stdout, stderr, struct {
			envelope
			Targets map[string][]Target `json:"targets"`
			Notes   []string            `json:"notes"`
		}{newEnvelope("suggest", nil), targets, notes}, nil)
	}
	for _, n := range notes {
		fmt.Fprintln(stdout, "note     "+n)
	}
	for _, typ := range []string{"disk", "service", "container", "port"} {
		fmt.Fprintf(stdout, "%s:\n", typ)
		if len(targets[typ]) == 0 {
			fmt.Fprintln(stdout, "  (none found)")
		}
		for _, t := range targets[typ] {
			fmt.Fprintf(stdout, "  %-32s %s\n", t.Value, t.Note)
		}
	}
	return exitOK
}

// suggestTargets turns what the server has into targets by monitor type. Every
// type is present, with an empty list when nothing was found.
func suggestTargets(inv host.Inventory, containers []string) map[string][]Target {
	out := map[string][]Target{"disk": {}, "service": {}, "container": {}, "port": {}}
	for _, f := range inv.Filesystems {
		d := f.Disk
		parts := []string{}
		for _, s := range []string{f.Device, f.Type, gigabytes(d.TotalBytes)} {
			if s != "" {
				parts = append(parts, s)
			}
		}
		if total := d.UsedBytes + d.FreeBytes; total > 0 {
			parts = append(parts, fmt.Sprintf("%d%% used", int(float64(d.UsedBytes)/float64(total)*100+0.5)))
		}
		note := strings.Join(parts, ", ")
		out["disk"] = append(out["disk"], Target{Value: f.Path, Note: note})
	}
	for _, s := range inv.Services {
		out["service"] = append(out["service"], Target{Value: s})
	}
	sorted := append([]string(nil), containers...)
	sort.Strings(sorted)
	for _, c := range sorted {
		out["container"] = append(out["container"], Target{Value: c})
	}
	seen := map[int]bool{}
	for _, lp := range inv.Ports {
		if seen[lp.Port] {
			continue
		}
		seen[lp.Port] = true
		where := "every address"
		switch lp.Address {
		case "", "0.0.0.0", "::":
		case "127.0.0.1", "::1":
			where = "this server only"
		default:
			where = lp.Address
		}
		out["port"] = append(out["port"], Target{Value: strconv.Itoa(lp.Port), Note: "listening on " + where})
	}
	sort.Slice(out["port"], func(i, j int) bool {
		a, _ := strconv.Atoi(out["port"][i].Value)
		b, _ := strconv.Atoi(out["port"][j].Value)
		return a < b
	})
	return out
}

func gigabytes(b uint64) string {
	return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + " GB"
}
