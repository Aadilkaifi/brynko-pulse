// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/changes"
	"github.com/Aadilkaifi/brynko-pulse/internal/duration"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

const changesDetails = `While it runs, pulse keeps a timeline of what changed on the machine, newest first:
commands run with sudo and sign-ins (from the journal), packages (apt), services, restarts and
software (the Windows event log), containers and images (Docker), and edits to common
configuration files, with the lines that changed. The same change within an hour is listed once.
Changes are kept for 90 days. Passwords and keys are hidden.`

// changeKinds are the kinds --kind accepts, in the order help lists them.
var changeKinds = []string{host.ChangeSudo, host.ChangeLogin, host.ChangePackage, host.ChangeService,
	host.ChangeReboot, host.ChangeContainer, host.ChangeFile}

// maxDetailLines is how much of a change's detail the text output shows; --json has all of it.
const maxDetailLines = 6

func cmdChanges(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("changes", stderr)
	dataDir := fs.String("data", platformFor().DataDir(), "where state and alert history are kept")
	sinceText := fs.String("since", "24h", "how far back to look, for example 6h, 7d or 30d (at most 90d)")
	kindText := fs.String("kind", "", "only these kinds, comma separated: "+strings.Join(changeKinds, ", "))
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(reorderFlags(args)) != nil {
		return exitUsage
	}
	since, err := duration.Parse(*sinceText)
	if err == nil && (since <= 0 || since > changes.Keep) {
		err = errors.New("--since must be between 1m and 90d")
	}
	var kinds []string
	if err == nil {
		kinds, err = parseKinds(*kindText)
	}
	from := time.Now().Add(-since)
	var list []host.Change
	if err == nil {
		list, err = changes.Read(filepath.Join(*dataDir, "changes"), from)
	}
	if len(kinds) > 0 {
		list = slices.DeleteFunc(list, func(c host.Change) bool { return !slices.Contains(kinds, c.Kind) })
	}
	if *asJSON {
		if list == nil {
			list = []host.Change{}
		}
		return finishJSON(stdout, stderr, struct {
			envelope
			Since   time.Time     `json:"since"`
			Changes []host.Change `json:"changes"`
		}{newEnvelope("changes", err), from.UTC().Truncate(time.Second), list}, err)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	if len(list) == 0 {
		fmt.Fprintf(stdout, "No changes in the last %s.\n", *sinceText)
		return exitOK
	}
	printChanges(stdout, list, *sinceText)
	return exitOK
}

func parseKinds(text string) ([]string, error) {
	var out []string
	for _, k := range strings.Split(text, ",") {
		if k = strings.ToLower(strings.TrimSpace(k)); k == "" {
			continue
		}
		if !slices.Contains(changeKinds, k) {
			return nil, fmt.Errorf("--kind %q is not one of: %s", k, strings.Join(changeKinds, ", "))
		}
		out = append(out, k)
	}
	return out, nil
}

func printChanges(w io.Writer, list []host.Change, since string) {
	noun := "changes"
	if len(list) == 1 {
		noun = "change"
	}
	fmt.Fprintf(w, "%d %s in the last %s, newest first:\n\n", len(list), noun, since)
	whoWidth := 3
	for _, c := range list {
		whoWidth = max(whoWidth, min(len(c.Who), 20))
	}
	indent := strings.Repeat(" ", 12+2+9+2+whoWidth+2)
	for _, c := range list {
		who := c.Who
		if who == "" {
			who = "-"
		}
		fmt.Fprintf(w, "%-12s  %-9s  %-*s  %s\n", c.At.Local().Format("2 Jan 15:04"), c.Kind, whoWidth, who, c.What)
		if c.Detail == "" {
			continue
		}
		lines := strings.Split(c.Detail, "\n")
		for _, l := range lines[:min(len(lines), maxDetailLines)] {
			fmt.Fprintln(w, indent+l)
		}
		if len(lines) > maxDetailLines {
			fmt.Fprintf(w, "%s(%d more lines in --json)\n", indent, len(lines)-maxDetailLines)
		}
	}
}
