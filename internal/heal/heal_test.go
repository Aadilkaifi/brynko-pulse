// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package heal

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/engine"
)

var t0 = time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

func plan() Plan {
	return Plan{Action: config.HealRestart, Attempts: 3, Within: time.Hour, Verify: 2 * time.Minute,
		After: 30 * time.Second, Timeout: time.Minute, What: "restart nginx", Done: "restarted nginx"}
}

// sim drives a Tracker the way the runner does, with the engine deciding firing and resolved.
type sim struct {
	t    *testing.T
	tr   *Tracker
	eng  *engine.Engine
	p    Plan
	sent []Step // steps that sent something
	runs int    // fixes started
}

func newSim(t *testing.T) *sim {
	return &sim{t: t, tr: NewTracker(10), eng: engine.New(nil), p: plan()}
}

// check observes one result at time at and returns what self-healing decided.
func (s *sim) check(at time.Time, bad bool, blocked string) Step {
	ev := s.eng.Observe("web", engine.Rule{Cooldown: 15 * time.Minute}, at, engine.Observation{Bad: bad, Detail: map[bool]string{true: "stopped", false: "running"}[bad]})
	st := s.tr.Step("web", s.p, at, s.eng.Get("web"), ev, blocked)
	if st.Send != nil {
		s.sent = append(s.sent, st)
	}
	if st.Start {
		s.runs++
	}
	return st
}

func (s *sim) done(at time.Time, err error) {
	s.tr.Done("web", Outcome{At: at, Err: err, Fatal: err != nil && strings.Contains(err.Error(), "not allowed")}, s.p.Verify)
}

func TestAFixThatWorksSendsOneHealedAlert(t *testing.T) {
	s := newSim(t)
	if st := s.check(t0, true, ""); st.Send != nil || st.Start {
		t.Fatalf("first bad reading: %+v (nothing is sent and nothing is tried yet)", st)
	}
	if st := s.check(t0.Add(30*time.Second), true, ""); !st.Start || st.Attempt != 1 || st.Send != nil {
		t.Fatalf("after 30s: %+v, want the first fix and no alert yet", st)
	}
	s.done(t0.Add(35*time.Second), nil)
	if st := s.check(t0.Add(40*time.Second), false, ""); st.Send == nil || st.Kind != alert.KindHealed ||
		!strings.Contains(st.Note, "It failed (stopped). pulse restarted nginx at") {
		t.Fatalf("recovered: %+v", st)
	}
	if len(s.sent) != 1 || s.runs != 1 {
		t.Fatalf("sent %d alerts and ran %d fixes, want one of each", len(s.sent), s.runs)
	}
}

func TestAFixThatDoesNotHelpTellsPeopleAndTriesAgain(t *testing.T) {
	s := newSim(t)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), nil)
	if st := s.check(t0.Add(time.Minute), true, ""); st.Send != nil || st.Start {
		t.Fatalf("while verifying: %+v, want to wait", st)
	}
	st := s.check(t0.Add(31*time.Second+2*time.Minute), true, "")
	if !st.Start || st.Attempt != 2 || st.Send == nil || st.Send.Kind != engine.EventFiring ||
		!strings.Contains(st.Note, "still fails, so it tries again (2 of 3)") {
		t.Fatalf("after a failed fix: %+v", st)
	}
	s.done(t0.Add(3*time.Minute), nil)
	if st := s.check(t0.Add(3*time.Minute+10*time.Second), false, ""); st.Kind != "" || st.Send.Kind != engine.EventResolved ||
		!strings.Contains(st.Note, "pulse restarted nginx at") {
		t.Fatalf("fixed by the second try: %+v (an ordinary resolved message, since the failure was already sent)", st)
	}
}

func TestPulseGivesUpAfterItsAttemptsAndSaysSoOnce(t *testing.T) {
	s := newSim(t)
	at := t0
	s.check(at, true, "")
	for i := 1; i <= 3; i++ {
		at = at.Add(30 * time.Second)
		if st := s.check(at, true, ""); !st.Start || st.Attempt != i {
			t.Fatalf("try %d: %+v", i, st)
		}
		s.done(at, errors.New("unit failed to start"))
	}
	at = at.Add(time.Second)
	st := s.check(at, true, "")
	if st.Start || st.Kind != alert.KindHealFailed || st.Send == nil ||
		!strings.Contains(st.Note, "tried to restart nginx 3 times in 1h and it still fails") ||
		!strings.Contains(st.Note, "unit failed to start") {
		t.Fatalf("give up: %+v", st)
	}
	if st := s.check(at.Add(time.Minute), true, ""); st.Send != nil || st.Start {
		t.Fatalf("after giving up: %+v, want quiet until the cooldown repeats", st)
	}
	if st := s.check(at.Add(2*time.Minute), false, ""); st.Send == nil || st.Send.Kind != engine.EventResolved {
		t.Fatalf("recovered after giving up: %+v", st)
	}
	// A new problem within the hour has no attempts left: it gives up at once.
	s.check(at.Add(10*time.Minute), true, "")
	if st := s.check(at.Add(11*time.Minute), true, ""); st.Start || st.Kind != alert.KindHealFailed {
		t.Fatalf("new problem, no attempts left: %+v", st)
	}
}

func TestABlipShorterThanTheDelaySendsNothing(t *testing.T) {
	s := newSim(t)
	s.check(t0, true, "")
	s.check(t0.Add(10*time.Second), false, "")
	if len(s.sent) != 0 || s.runs != 0 {
		t.Fatalf("sent %v, ran %d: a 10-second blip is not worth a fix or an alert", s.sent, s.runs)
	}
}

func TestNoFixInAMaintenanceWindow(t *testing.T) {
	s := newSim(t)
	// The alert goes out at once, as without a heal rule: pulse is not going to try.
	st := s.check(t0, true, "the maintenance window deploys is open")
	if st.Start || st.Send == nil || !strings.Contains(st.Note, "did not try to fix it: the maintenance window deploys is open") {
		t.Fatalf("maintenance: %+v", st)
	}
	if st := s.check(t0.Add(time.Minute), true, "the maintenance window deploys is open"); st.Start || st.Send != nil {
		t.Fatalf("still in maintenance: %+v", st)
	}
	if s.runs != 0 {
		t.Fatal("a fix ran in a maintenance window")
	}
}

func TestAMissingRightGivesUpAtOnce(t *testing.T) {
	s := newSim(t)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), errors.New("pulse is not allowed to restart this service: nginx"))
	st := s.check(t0.Add(32*time.Second), true, "")
	if st.Start || st.Kind != alert.KindHealFailed || !strings.Contains(st.Note, "could not restart nginx: pulse is not allowed") {
		t.Fatalf("missing right: %+v", st)
	}
	if s.runs != 1 {
		t.Fatalf("ran %d fixes, want 1: trying again cannot help", s.runs)
	}
}

func TestTheServerWideCapStopsFixes(t *testing.T) {
	s := newSim(t)
	s.tr.SetLimit(1)
	s.p.Attempts = 5
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), nil)
	st := s.check(t0.Add(3*time.Minute), true, "")
	if st.Start || st.Kind != alert.KindHealFailed || !strings.Contains(st.Note, "limit of 1 fixes an hour") {
		t.Fatalf("server cap: %+v", st)
	}
}

func TestAFixThatEndsAfterRecoveryLeavesNoWait(t *testing.T) {
	s := newSim(t)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")  // fix starts
	s.check(t0.Add(32*time.Second), false, "") // it came back by itself meanwhile
	s.done(t0.Add(40*time.Second), nil)        // the fix answers late
	s.check(t0.Add(50*time.Second), true, "")  // a new problem
	if st := s.check(t0.Add(80*time.Second), true, ""); !st.Start {
		t.Fatalf("new problem after a late fix: %+v, want a new try (no leftover wait)", st)
	}
}

func TestStatus(t *testing.T) {
	s := newSim(t)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	if hs := s.tr.Status("web", s.p, t0.Add(31*time.Second)); !hs.Healing || hs.AttemptsUsed != 1 || hs.Attempts != 3 {
		t.Fatalf("while fixing: %+v", hs)
	}
	s.done(t0.Add(35*time.Second), errors.New("boom"))
	hs := s.tr.Status("web", s.p, t0.Add(36*time.Second))
	if hs.Healing || hs.LastResult != "Tried to restart nginx (attempt 0): boom" || hs.LastAt.IsZero() {
		t.Fatalf("after a failed fix: %+v", hs)
	}
}

func TestPlanFor(t *testing.T) {
	m := config.Monitor{ID: "web", Type: "service", Service: "nginx", For: config.Duration(2 * time.Minute),
		Heal: &config.Heal{Action: config.HealRestart}}
	p, ok := PlanFor(m)
	if !ok || p.After != 2*time.Minute || p.Attempts != 3 || p.Verify != 2*time.Minute || p.What != "restart nginx" {
		t.Fatalf("plan = %+v", p)
	}
	m.For = 0
	if p, _ := PlanFor(m); p.After != MinDelay {
		t.Fatalf("for 0: after = %v, want the minimum %v", p.After, MinDelay)
	}
	c := config.Monitor{ID: "c", Type: "container", Container: "api", Heal: &config.Heal{Action: config.HealRestart}}
	if p, _ := PlanFor(c); p.Done != "restarted the container api" {
		t.Fatalf("container: %+v", p)
	}
	r := config.Monitor{ID: "q", Type: "command", Run: []string{"x"}, Heal: &config.Heal{Action: config.HealRun, Run: []string{"/usr/local/bin/drain-queue", "--all"}}}
	if p, _ := PlanFor(r); p.What != "run drain-queue" {
		t.Fatalf("run: %+v", p)
	}
	off := false
	m.Enabled = &off
	if _, ok := PlanFor(m); ok {
		t.Fatal("a monitor that is off has no plan")
	}
}

// Bug scan 2026-10-17: a missing right belongs to one problem. Once an admin grants it,
// the next problem is tried again instead of being given up at once.
func TestAMissingRightDoesNotStickToTheNextProblem(t *testing.T) {
	s := newSim(t)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), errors.New("pulse is not allowed to restart this service: nginx"))
	s.check(t0.Add(32*time.Second), true, "") // gives up
	s.check(t0.Add(time.Minute), false, "")   // it works again (someone fixed it by hand)
	s.check(t0.Add(10*time.Minute), true, "")
	if st := s.check(t0.Add(11*time.Minute), true, ""); !st.Start {
		t.Fatalf("new problem after the right was granted: %+v, want a new try", st)
	}
}

// Bug scan: a reminder that is due while a fix runs, after the alert went out, is sent.
func TestAReminderDuringAFixIsNotSwallowed(t *testing.T) {
	s := newSim(t)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), nil)
	s.check(t0.Add(31*time.Second+2*time.Minute), true, "") // second try: the alert goes out now
	remind := &engine.Event{Kind: engine.EventFiring, Monitor: "web", Detail: "Still failing after the silence ended. stopped", At: t0.Add(3 * time.Minute)}
	st := s.tr.Step("web", s.p, t0.Add(3*time.Minute), s.eng.Get("web"), remind, "")
	if st.Send != remind || !strings.Contains(st.Note, "pulse is trying to restart nginx") {
		t.Fatalf("reminder while fixing: %+v", st)
	}
}

// Bug scan: a flapping notice is not replaced by the held failure it describes.
func TestAFlappingNoticeWinsOverAHeldFailure(t *testing.T) {
	s := newSim(t)
	s.check(t0, true, "") // held
	flap := &engine.Event{Kind: engine.EventFlapping, Monitor: "web", Detail: "It changed between failing and recovered 5 times", At: t0.Add(5 * time.Second)}
	st := s.tr.Step("web", s.p, t0.Add(5*time.Second), s.eng.Get("web"), flap, "it keeps switching between failing and working")
	if st.Send != flap || st.Start {
		t.Fatalf("flapping: %+v, want the flapping notice and no fix", st)
	}
}

// Bug scan: a recovery the engine did not announce (it was flapping) still ends the problem,
// so the next problem starts clean instead of reusing the old held alert.
func TestAnUnannouncedRecoveryEndsTheProblem(t *testing.T) {
	s := newSim(t)
	s.check(t0, true, "") // held, detail "stopped"
	ok := engine.State{Status: engine.OK, LastChecked: t0.Add(10 * time.Second)}
	s.tr.Step("web", s.p, t0.Add(10*time.Second), ok, nil, "")
	if got := s.tr.Take("web"); got != nil {
		t.Fatalf("the old held alert survived the recovery: %+v", got)
	}
}

// Bug scan: when a heal rule is removed while its alert is held, the alert is handed back.
func TestTakeReturnsAHeldAlert(t *testing.T) {
	s := newSim(t)
	s.check(t0, true, "")
	if got := s.tr.Take("web"); got == nil || got.Kind != engine.EventFiring {
		t.Fatalf("take = %+v, want the held firing alert", got)
	}
	if got := s.tr.Take("web"); got != nil {
		t.Fatal("take must forget the monitor")
	}
}

// Bug scan: a problem that started before pulse did (pulse restarted) was already reported:
// a fix that works then is an ordinary resolved message, not "it failed ... fixed".
func TestAProblemFromBeforeARestartIsNotCalledFixedOutOfTheBlue(t *testing.T) {
	tr := NewTracker(10)
	p := plan()
	firing := engine.State{Status: engine.Firing, ProblemSince: t0.Add(-time.Hour), Detail: "stopped"}
	if st := tr.Step("web", p, t0, firing, nil, ""); !st.Start {
		t.Fatalf("after a restart: %+v, want a try", st)
	}
	tr.Done("web", Outcome{At: t0.Add(time.Second)}, p.Verify)
	resolved := &engine.Event{Kind: engine.EventResolved, Monitor: "web", Detail: "running", At: t0.Add(10 * time.Second)}
	st := tr.Step("web", p, t0.Add(10*time.Second), engine.State{Status: engine.OK}, resolved, "")
	if st.Kind != "" || st.Send != resolved || !strings.Contains(st.Note, "pulse restarted nginx at") {
		t.Fatalf("resolved: %+v", st)
	}
}
