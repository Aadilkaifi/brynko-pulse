// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/discover"
	"github.com/Aadilkaifi/brynko-pulse/internal/monitor"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
)

func cmdDiscover(args []string, stdout, stderr io.Writer) int {
	p := platformFor()
	fs := newFlags("discover", stderr)
	cfgDir := fs.String("config", p.ConfigDir(), "configuration directory")
	apply := fs.Bool("apply", false, "save every suggestion as monitors/<id>.yaml")
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
	list, notes, err := suggestMonitors(ctx, p, *cfgDir)
	saved, reloaded := false, false
	if err == nil && *apply && len(list) > 0 {
		if err = saveSuggestions(*cfgDir, list); err == nil {
			saved = true
			reloaded, err = p.ReloadService(ctx)
			if err != nil {
				err = fmt.Errorf("saved, but the running service could not be reloaded: %w", err)
			}
		}
	}

	if *asJSON {
		if list == nil {
			list = []discover.Suggestion{}
		}
		if notes == nil {
			notes = []string{}
		}
		return finishJSON(stdout, stderr, struct {
			envelope
			Suggestions []discover.Suggestion `json:"suggestions"`
			Notes       []string              `json:"notes"`
			Saved       bool                  `json:"saved"`
			Reloaded    bool                  `json:"reloaded"`
		}{newEnvelope("discover", err), list, notes, saved, reloaded}, err)
	}
	for _, n := range notes {
		fmt.Fprintln(stdout, "note     "+n)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "Everything found is already monitored.")
		return exitOK
	}
	fmt.Fprintf(stdout, "Found %d thing(s) to monitor:\n\n", len(list))
	for _, s := range list {
		fmt.Fprintf(stdout, "  %-32s %s\n", s.ID, s.Why)
	}
	switch {
	case !saved:
		fmt.Fprintln(stdout, "\nNothing was saved. Save them all with: pulse discover --apply\nor add the ones you want with pulse add.")
	case reloaded:
		fmt.Fprintln(stdout, "\nSaved, one file per monitor in monitors/. The running service picked them up.")
	default:
		fmt.Fprintln(stdout, "\nSaved, one file per monitor in monitors/. They apply at the next start.")
	}
	return exitOK
}

// suggestMonitors runs discovery against the configuration in cfgDir.
func suggestMonitors(ctx context.Context, p platform.Platform, cfgDir string) ([]discover.Suggestion, []string, error) {
	cfg, res := config.Load(cfgDir)
	if !res.OK() {
		return nil, nil, errors.New("the configuration has errors; fix them first (run pulse validate)")
	}
	inv, err := p.Inventory(ctx)
	if err != nil {
		return nil, nil, err
	}
	var notes []string
	containers, err := monitor.ListContainers(ctx)
	if err != nil {
		notes = append(notes, "containers were skipped: "+err.Error())
	}
	list, skipped := discover.Suggest(inv, containers, cfg, p.RootDisk())
	return list, append(notes, skipped...), nil
}

// saveSuggestions writes each suggestion as its own monitor file through config.Apply.
func saveSuggestions(cfgDir string, list []discover.Suggestion) error {
	changes := make([]config.Change, 0, len(list))
	for _, s := range list {
		changes = append(changes, config.Change{Path: s.File, Data: []byte(s.YAML), CreateOnly: true})
	}
	vr, err := config.Apply(cfgDir, changes)
	if errors.Is(err, config.ErrInvalid) && len(vr.Errors) > 0 {
		err = fmt.Errorf("%w: %s", err, formatIssue(vr.Errors[0]))
	}
	return err
}
