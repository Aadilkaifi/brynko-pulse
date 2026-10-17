// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package heal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// fakePlatform records restarts; run fixes start real programs.
type fakePlatform struct {
	platform.Platform
	restarted []string
	err       error
}

func (f *fakePlatform) RestartService(_ context.Context, name string) error {
	f.restarted = append(f.restarted, name)
	return f.err
}

func (f *fakePlatform) PrepareCommand(*exec.Cmd) error { return nil }

// TestHelperExit is not a test: run fixes start this binary with "--" and an exit
// code, so the tests need no shell and no program that differs by system.
func TestHelperExit(t *testing.T) {
	args := os.Args
	for i, a := range args {
		if a == "--" && i+1 < len(args) {
			code, _ := strconv.Atoi(args[i+1])
			fmt.Println("drained 3 jobs")
			os.Exit(code)
		}
	}
}

func helper(code int) []string {
	return []string{os.Args[0], "-test.run=^TestHelperExit$", "--", strconv.Itoa(code)}
}

func TestFixRestartsTheServiceTheMonitorWatches(t *testing.T) {
	f := &fakePlatform{}
	m := config.Monitor{ID: "web", Type: "service", Service: "nginx", Heal: &config.Heal{Action: config.HealRestart}}
	p, _ := PlanFor(m)
	if o := (Fixer{Platform: f}).Fix(context.Background(), m, p); o.Err != nil || o.At.IsZero() {
		t.Fatalf("outcome = %+v", o)
	}
	if strings.Join(f.restarted, ",") != "nginx" {
		t.Fatalf("restarted %v", f.restarted)
	}
}

func TestAMissingRightIsFatal(t *testing.T) {
	f := &fakePlatform{err: fmt.Errorf("%w: nginx", host.ErrHealNotAllowed)}
	m := config.Monitor{ID: "web", Type: "service", Service: "nginx", Heal: &config.Heal{Action: config.HealRestart}}
	p, _ := PlanFor(m)
	if o := (Fixer{Platform: f}).Fix(context.Background(), m, p); !o.Fatal || !errors.Is(o.Err, host.ErrHealNotAllowed) {
		t.Fatalf("outcome = %+v", o)
	}
	f.err = errors.New("Job for nginx.service failed")
	if o := (Fixer{Platform: f}).Fix(context.Background(), m, p); o.Fatal || o.Err == nil {
		t.Fatalf("a failed start may work next time: %+v", o)
	}
}

func TestARunFixSucceedsOrSaysWhy(t *testing.T) {
	m := config.Monitor{ID: "q", Type: "command", Run: []string{"x"}}
	for _, tc := range []struct {
		code int
		ok   bool
	}{{0, true}, {3, false}} {
		m.Heal = &config.Heal{Action: config.HealRun, Run: helper(tc.code), Timeout: config.Duration(30 * time.Second)}
		p, _ := PlanFor(m)
		o := (Fixer{Platform: &fakePlatform{}, OutputLimit: 1024}).Fix(context.Background(), m, p)
		if (o.Err == nil) != tc.ok || o.Fatal || !strings.Contains(o.Output, "drained 3 jobs") {
			t.Fatalf("exit %d: %+v", tc.code, o)
		}
		if !tc.ok && !strings.Contains(o.Err.Error(), "exited with code 3: drained 3 jobs") {
			t.Fatalf("exit 3: %v", o.Err)
		}
	}
}
