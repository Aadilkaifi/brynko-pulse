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

func plannedFix(t *testing.T, steps ...config.HealStep) Plan {
	t.Helper()
	m := config.Monitor{ID: "web", Type: "http", Heal: &config.Heal{Steps: steps, Timeout: config.Duration(30 * time.Second)}}
	p, ok := PlanFor(m)
	if !ok {
		t.Fatal("no plan")
	}
	return p
}

func TestFixRunsTheStepsInOrder(t *testing.T) {
	f := &fakePlatform{}
	p := plannedFix(t, config.HealStep{RestartService: []string{"php8.3-fpm", "nginx"}}, config.HealStep{Run: helper(0)})
	o := (Fixer{Platform: f, OutputLimit: 1024}).Fix(context.Background(), p)
	if o.Err != nil || o.At.IsZero() || !strings.Contains(o.Output, "drained 3 jobs") {
		t.Fatalf("outcome = %+v", o)
	}
	if strings.Join(f.restarted, ",") != "php8.3-fpm,nginx" {
		t.Fatalf("restarted %v", f.restarted)
	}
}

func TestFixStopsAtTheFirstStepThatFails(t *testing.T) {
	f := &fakePlatform{err: errors.New("Job for php8.3-fpm.service failed")}
	p := plannedFix(t, config.HealStep{RestartService: []string{"php8.3-fpm", "nginx"}}, config.HealStep{Run: helper(0)})
	o := (Fixer{Platform: f, OutputLimit: 1024}).Fix(context.Background(), p)
	if o.Err == nil || o.Fatal || !strings.Contains(o.Err.Error(), "restart php8.3-fpm: Job for") || o.Output != "" {
		t.Fatalf("outcome = %+v", o)
	}
	if strings.Join(f.restarted, ",") != "php8.3-fpm" {
		t.Fatalf("restarted %v, want to stop after the first failure", f.restarted)
	}
}

func TestAMissingRightIsFatal(t *testing.T) {
	f := &fakePlatform{err: fmt.Errorf("%w: nginx", host.ErrHealNotAllowed)}
	p := plannedFix(t, config.HealStep{RestartService: []string{"nginx"}})
	if o := (Fixer{Platform: f}).Fix(context.Background(), p); !o.Fatal || !errors.Is(o.Err, host.ErrHealNotAllowed) {
		t.Fatalf("outcome = %+v", o)
	}
}

func TestARunStepSucceedsOrSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		code int
		ok   bool
	}{{0, true}, {3, false}} {
		p := plannedFix(t, config.HealStep{Run: helper(tc.code)})
		o := (Fixer{Platform: &fakePlatform{}, OutputLimit: 1024}).Fix(context.Background(), p)
		if (o.Err == nil) != tc.ok || o.Fatal || !strings.Contains(o.Output, "drained 3 jobs") {
			t.Fatalf("exit %d: %+v", tc.code, o)
		}
		if !tc.ok && !strings.Contains(o.Err.Error(), "exited with code 3: drained 3 jobs") {
			t.Fatalf("exit 3: %v", o.Err)
		}
	}
}

func TestPlanWordsAndModes(t *testing.T) {
	p := plannedFix(t, config.HealStep{RestartService: []string{"php8.3-fpm", "nginx"}},
		config.HealStep{RestartContainer: []string{"api"}}, config.HealStep{Run: []string{`C:\fix\clear.ps1`}})
	if p.What != "restart php8.3-fpm and nginx, then restart the container api, then run clear.ps1" ||
		p.Done != "restarted php8.3-fpm and nginx, then restarted the container api, then ran clear.ps1" {
		t.Fatalf("words: %q / %q", p.What, p.Done)
	}
	if p.Mode != config.HealAsk || p.Approve != "sudo pulse fix approve web" {
		t.Fatalf("plan = %+v, want ask by default", p)
	}
	off := config.Monitor{ID: "x", Type: "http", Heal: &config.Heal{Mode: config.HealOff, Steps: []config.HealStep{{Run: []string{"x"}}}}}
	if _, ok := PlanFor(off); ok {
		t.Fatal("mode off must have no plan")
	}
}
