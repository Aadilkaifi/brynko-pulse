// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/heal"
)

const fixDetails = `Examples:
  pulse fix approve web     run the fix waiting for monitor web
  pulse fix dismiss web     turn it down; no other fix is offered until web works again

A fix waits for approval when its monitor's heal mode is ask, or when automatic
fixes did not help. pulse status shows what it would do. Use --id to approve only
the fix that status showed, not a newer one.`

// fixWait is how long pulse fix waits for the running service to answer.
const fixWait = 10 * time.Second

func cmdFix(args []string, stdout, stderr io.Writer) int {
	p := platformFor()
	fs := newFlags("fix", stderr)
	dataDir := fs.String("data", p.DataDir(), "where state and alert history are kept")
	id := fs.String("id", "", "approve or dismiss only this fix (from pulse status --json)")
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(reorderFlags(args)) != nil {
		return exitUsage
	}
	a := fs.Args()
	if len(a) != 2 || (a[0] != heal.ActionApprove && a[0] != heal.ActionDismiss) || !idRe.MatchString(a[1]) {
		fs.Usage()
		return exitUsage
	}
	action, monitor := a[0], a[1]
	ans, err := heal.Submit(context.Background(), *dataDir,
		heal.Request{Monitor: monitor, FixID: *id, Action: action, At: time.Now()}, fixWait)
	if err == nil && !ans.OK {
		err = fmt.Errorf("%s", ans.Error)
	}
	if *asJSON {
		return finishJSON(stdout, stderr, struct {
			envelope
			Monitor string `json:"monitor"`
			Action  string `json:"action"`
			FixID   string `json:"fix_id,omitempty"`
			What    string `json:"what,omitempty"`
		}{newEnvelope("fix", err), monitor, action, ans.FixID, ans.What}, err)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	if action == heal.ActionApprove {
		fmt.Fprintf(stdout, "Approved. pulse will %s now and tell you how it went.\n", ans.What)
	} else {
		fmt.Fprintf(stdout, "Dismissed. pulse will not offer another fix for %s until it works again.\n", monitor)
	}
	return exitOK
}
