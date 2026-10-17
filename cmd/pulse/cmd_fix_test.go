// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/heal"
)

func TestFixUsage(t *testing.T) {
	for _, args := range [][]string{{"fix"}, {"fix", "restart", "web"}, {"fix", "approve"}, {"fix", "approve", "bad id!"}} {
		if code, _, _ := runCmd(args...); code != exitUsage {
			t.Errorf("%v: exit %d, want usage", args, code)
		}
	}
}

func TestFixWithoutTheServiceSaysSo(t *testing.T) {
	code, _, errOut := runCmd("fix", "approve", "web", "--data", t.TempDir())
	if code != exitInvalid || !strings.Contains(errOut, "pulse did not answer") {
		t.Fatalf("exit %d, %q", code, errOut)
	}
}

// The command hands the request to the running service and prints its answer.
func TestFixApproveAndDismiss(t *testing.T) {
	data := t.TempDir()
	heal.PrepareRequests(data)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				heal.Serve(data, time.Now(), func(r heal.Request) heal.Answer {
					if r.Monitor != "web" || r.FixID != "fix1" {
						return heal.Answer{Error: heal.ErrNotWaiting.Error()}
					}
					return heal.Answer{OK: true, FixID: r.FixID, What: "restart nginx"}
				})
			}
		}
	}()
	code, out, errOut := runCmd("fix", "approve", "web", "--id", "fix1", "--data", data)
	if code != exitOK || !strings.Contains(out, "Approved. pulse will restart nginx now") {
		t.Fatalf("approve: %d %q %q", code, out, errOut)
	}
	code, out, _ = runCmd("fix", "dismiss", "web", "--id", "fix1", "--data", data, "--json")
	var body struct {
		OK     bool   `json:"ok"`
		Action string `json:"action"`
		FixID  string `json:"fix_id"`
	}
	if code != exitOK || json.Unmarshal([]byte(out), &body) != nil || !body.OK || body.Action != "dismiss" || body.FixID != "fix1" {
		t.Fatalf("dismiss --json: %d %s", code, out)
	}
	code, _, errOut = runCmd("fix", "approve", "web", "--id", "old", "--data", data)
	if code != exitInvalid || !strings.Contains(errOut, "no longer waiting") {
		t.Fatalf("stale id: %d %q", code, errOut)
	}
}
