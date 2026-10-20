// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package heal

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/engine"
)

var t0 = time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

func plan(mode string) Plan {
	return Plan{Mode: mode, Steps: []config.HealStep{{RestartService: []string{"nginx"}}}, Attempts: 3, Within: time.Hour,
		Verify: 2 * time.Minute, After: 30 * time.Second, Timeout: time.Minute, What: "restart nginx", Done: "restarted nginx",
		Approve: "sudo pulse fix approve web"}
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

func newSim(t *testing.T, mode string) *sim {
	tr := NewTracker(10)
	n := 0
	tr.newID = func() string { n++; return fmt.Sprintf("fix%d", n) }
	return &sim{t: t, tr: tr, eng: engine.New(nil), p: plan(mode)}
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

func (s *sim) done(at time.Time, err error, approved bool) {
	s.tr.Done("web", Outcome{At: at, Err: err, Approved: approved,
		Fatal: err != nil && strings.Contains(err.Error(), "not allowed")}, s.p.Verify)
}

// ── auto ─────────────────────────────────────────────────────────────────────

func TestAutoFixThatWorksSendsOneHealedAlert(t *testing.T) {
	s := newSim(t, config.HealAuto)
	if st := s.check(t0, true, ""); st.Send != nil || st.Start {
		t.Fatalf("first bad reading: %+v (nothing is sent and nothing is tried yet)", st)
	}
	if st := s.check(t0.Add(30*time.Second), true, ""); !st.Start || st.Attempt != 1 || st.Send != nil || st.Approved {
		t.Fatalf("after 30s: %+v, want the first fix and no alert yet", st)
	}
	s.done(t0.Add(35*time.Second), nil, false)
	if st := s.check(t0.Add(40*time.Second), false, ""); st.Send == nil || st.Kind != alert.KindHealed ||
		!strings.Contains(st.Note, "It failed (stopped). pulse restarted nginx at") {
		t.Fatalf("recovered: %+v", st)
	}
	if len(s.sent) != 1 || s.runs != 1 {
		t.Fatalf("sent %d alerts and ran %d fixes, want one of each", len(s.sent), s.runs)
	}
}

func TestAutoWaitsBeforeTryingAgainThenTellsPeople(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), nil, false) // verify until 2m31s, then a 1m pause
	if st := s.check(t0.Add(time.Minute), true, ""); st.Send != nil || st.Start {
		t.Fatalf("while verifying: %+v, want to wait", st)
	}
	if st := s.check(t0.Add(3*time.Minute), true, ""); st.Start {
		t.Fatalf("during the pause after a fix that did not help: %+v", st)
	}
	st := s.check(t0.Add(3*time.Minute+31*time.Second), true, "")
	if !st.Start || st.Attempt != 2 || st.Send == nil || st.Send.Kind != engine.EventFiring ||
		!strings.Contains(st.Note, "still fails, so it tries again (2 of 3)") {
		t.Fatalf("second try: %+v", st)
	}
	s.done(t0.Add(4*time.Minute), nil, false)
	if st := s.check(t0.Add(4*time.Minute+10*time.Second), false, ""); st.Kind != alert.KindHealed ||
		st.Send.Kind != engine.EventResolved || !strings.Contains(st.Note, "pulse restarted nginx at") {
		t.Fatalf("fixed by the second try: %+v", st)
	}
}

func TestAutoSwitchesToAskAfterTwoFixesThatDidNotHelp(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), nil, false)
	s.check(t0.Add(3*time.Minute+31*time.Second), true, "")
	s.done(t0.Add(4*time.Minute), nil, false) // verify until 6m
	if st := s.check(t0.Add(5*time.Minute), true, ""); st.Start || st.Send != nil {
		t.Fatalf("while verifying: %+v", st)
	}
	// No pause before asking: only automatic fixes wait between tries.
	st := s.check(t0.Add(7*time.Minute), true, "")
	if st.Start || st.Kind != alert.KindHealFailed || st.Offered != "fix1" || st.Send == nil ||
		!strings.Contains(st.Note, "restarted nginx 2 times and it still fails, so the next fix waits for your approval") ||
		!strings.Contains(st.Note, "Approve it with sudo pulse fix approve web") {
		t.Fatalf("after two fixes: %+v", st)
	}
	if s.runs != 2 {
		t.Fatalf("ran %d fixes, want 2 before asking", s.runs)
	}
	if _, err := s.tr.Approve("web", "fix1"); err != nil {
		t.Fatal(err)
	}
	if st := s.check(t0.Add(7*time.Minute+2*time.Second), true, ""); !st.Start || !st.Approved || st.Attempt != 3 {
		t.Fatalf("approved: %+v", st)
	}
	s.done(t0.Add(7*time.Minute+5*time.Second), nil, true)
	if st := s.check(t0.Add(8*time.Minute), false, ""); st.Kind != alert.KindHealed || !strings.Contains(st.Note, "after your approval") {
		t.Fatalf("fixed after approval: %+v", st)
	}
}

func TestTheServerWideCapSendsTheFixForApproval(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.tr.SetLimit(1)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), nil, false)
	st := s.check(t0.Add(4*time.Minute), true, "")
	if st.Start || st.Offered == "" || !strings.Contains(st.Note, "limit of 1 automatic fixes an hour") {
		t.Fatalf("server cap: %+v", st)
	}
}

func TestUsedUpTriesSendTheFixForApproval(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.p.Attempts = 1
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), nil, false)
	st := s.check(t0.Add(4*time.Minute), true, "")
	if st.Start || st.Offered == "" || !strings.Contains(st.Note, "used the 1 automatic tries allowed in 1h") {
		t.Fatalf("tries used: %+v", st)
	}
}

// ── ask ──────────────────────────────────────────────────────────────────────

func TestAskOffersTheFixWithTheFirstAlertAndRunsItOnApproval(t *testing.T) {
	s := newSim(t, config.HealAsk)
	if st := s.check(t0, true, ""); st.Send != nil {
		t.Fatalf("first bad reading: %+v", st)
	}
	st := s.check(t0.Add(30*time.Second), true, "")
	if st.Start || st.Send == nil || st.Send.Kind != engine.EventFiring || st.Kind != "" || st.Offered != "fix1" ||
		!strings.Contains(st.Note, "A fix is waiting for your approval: restart nginx.") {
		t.Fatalf("offer: %+v", st)
	}
	if st := s.check(t0.Add(time.Minute), true, ""); st.Start || st.Send != nil {
		t.Fatalf("waiting: %+v, want nothing until someone answers", st)
	}
	if _, err := s.tr.Approve("web", "old-id"); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("approving another fix: %v", err)
	}
	if hs := s.tr.Status("web", s.p, t0.Add(time.Minute)); hs.Waiting == nil || hs.Waiting.ID != "fix1" || hs.Mode != config.HealAsk {
		t.Fatalf("status while waiting: %+v", hs)
	}
	w, err := s.tr.Approve("web", "fix1")
	if err != nil || w.ID != "fix1" {
		t.Fatalf("approve: %+v %v", w, err)
	}
	if st := s.check(t0.Add(2*time.Minute), true, ""); !st.Start || !st.Approved || st.Send != nil {
		t.Fatalf("after approval: %+v", st)
	}
	s.done(t0.Add(2*time.Minute+3*time.Second), nil, true)
	if st := s.check(t0.Add(2*time.Minute+10*time.Second), false, ""); st.Kind != alert.KindHealed ||
		!strings.Contains(st.Note, "pulse restarted nginx at") || !strings.Contains(st.Note, "after your approval and it works again") {
		t.Fatalf("fixed: %+v", st)
	}
	if s.runs != 1 || len(s.sent) != 2 {
		t.Fatalf("ran %d, sent %d: want one fix, the offer and the healed message", s.runs, len(s.sent))
	}
}

func TestAnApprovedFixThatDoesNotHelpOffersAnother(t *testing.T) {
	s := newSim(t, config.HealAsk)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.tr.Approve("web", "")
	s.check(t0.Add(31*time.Second), true, "")
	s.done(t0.Add(32*time.Second), nil, true)
	st := s.check(t0.Add(3*time.Minute), true, "")
	if st.Start || st.Kind != alert.KindHealFailed || st.Offered != "fix2" ||
		!strings.Contains(st.Note, "pulse did the fix (restarted nginx) but it still fails.") {
		t.Fatalf("after an approved fix that did not help: %+v", st)
	}
}

func TestDismissStopsOffersUntilTheProblemEnds(t *testing.T) {
	s := newSim(t, config.HealAsk)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	if _, err := s.tr.Dismiss("web", "fix1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.tr.Approve("web", "fix1"); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("a dismissed fix was approved: %v", err)
	}
	for _, at := range []time.Duration{time.Minute, 20 * time.Minute} {
		if st := s.check(t0.Add(at), true, ""); st.Start || st.Offered != "" {
			t.Fatalf("after dismiss at %v: %+v", at, st)
		}
	}
	s.check(t0.Add(30*time.Minute), false, "")
	s.check(t0.Add(40*time.Minute), true, "")
	if st := s.check(t0.Add(41*time.Minute), true, ""); st.Offered == "" {
		t.Fatalf("a new problem: %+v, want a new offer", st)
	}
}

func TestAWaitingFixIsPreparedAfreshAfterAnHour(t *testing.T) {
	s := newSim(t, config.HealAsk)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	if st := s.check(t0.Add(30*time.Second+WaitFor), true, ""); st.Start {
		t.Fatalf("expiry must not run anything: %+v", st)
	}
	if _, err := s.tr.Approve("web", "fix1"); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("the expired fix was approved: %v", err)
	}
	if hs := s.tr.Status("web", s.p, t0.Add(31*time.Second+WaitFor)); hs.Waiting == nil || hs.Waiting.ID != "fix2" {
		t.Fatalf("status after expiry: %+v", hs)
	}
}

func TestRecoveryWhileAFixWaitsSaysItIsNoLongerNeeded(t *testing.T) {
	s := newSim(t, config.HealAsk)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	st := s.check(t0.Add(time.Minute), false, "")
	if st.Send == nil || st.Send.Kind != engine.EventResolved || !strings.Contains(st.Note, "no longer needed") {
		t.Fatalf("recovered while waiting: %+v", st)
	}
	if hs := s.tr.Status("web", s.p, t0.Add(time.Minute)); hs.Waiting != nil {
		t.Fatalf("the fix still waits after recovery: %+v", hs)
	}
}

// ── both ─────────────────────────────────────────────────────────────────────

func TestABlipShorterThanTheDelaySendsNothing(t *testing.T) {
	for _, mode := range []string{config.HealAsk, config.HealAuto} {
		s := newSim(t, mode)
		s.check(t0, true, "")
		s.check(t0.Add(10*time.Second), false, "")
		if len(s.sent) != 0 || s.runs != 0 {
			t.Fatalf("%s: sent %v, ran %d: a 10-second blip is not worth a fix or an alert", mode, s.sent, s.runs)
		}
	}
}

func TestBlockedMeansNoFixAndNoOffer(t *testing.T) {
	for _, mode := range []string{config.HealAsk, config.HealAuto} {
		s := newSim(t, mode)
		// The alert goes out at once, as without a heal rule: pulse is not going to act.
		st := s.check(t0, true, "mysql is failing too, which is the likely cause")
		if st.Start || st.Offered != "" || st.Send == nil ||
			!strings.Contains(st.Note, "did not try to fix it: mysql is failing too, which is the likely cause") {
			t.Fatalf("%s blocked: %+v", mode, st)
		}
		if st := s.check(t0.Add(time.Minute), true, "mysql is failing too, which is the likely cause"); st.Send != nil || st.Start {
			t.Fatalf("%s still blocked: %+v", mode, st)
		}
		if st := s.check(t0.Add(2*time.Minute), true, ""); (mode == config.HealAuto) != st.Start || (mode == config.HealAsk) == (st.Offered == "") {
			t.Fatalf("%s once the cause works: %+v", mode, st)
		}
	}
}

func TestAnApprovedFixRunsEvenWhenBlocked(t *testing.T) {
	s := newSim(t, config.HealAsk)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.tr.Approve("web", "fix1")
	if st := s.check(t0.Add(time.Minute), true, "the maintenance window deploys is open"); !st.Start || !st.Approved {
		t.Fatalf("a person's approval in a maintenance window: %+v", st)
	}
}

func TestAMissingRightGivesUpAtOnce(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), errors.New("pulse is not allowed to restart this service: nginx"), false)
	st := s.check(t0.Add(32*time.Second), true, "")
	if st.Start || st.Offered != "" || st.Kind != alert.KindHealFailed || !strings.Contains(st.Note, "could not restart nginx: pulse is not allowed") {
		t.Fatalf("missing right: %+v", st)
	}
	if s.runs != 1 {
		t.Fatalf("ran %d fixes, want 1: trying again cannot help", s.runs)
	}
}

// A missing right belongs to one problem. Once an admin grants it, the next problem
// is tried again instead of being given up at once.
func TestAMissingRightDoesNotStickToTheNextProblem(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), errors.New("pulse is not allowed to restart this service: nginx"), false)
	s.check(t0.Add(32*time.Second), true, "") // gives up
	s.check(t0.Add(time.Minute), false, "")   // it works again (someone fixed it by hand)
	s.check(t0.Add(10*time.Minute), true, "")
	if st := s.check(t0.Add(11*time.Minute), true, ""); !st.Start {
		t.Fatalf("new problem after the right was granted: %+v, want a new try", st)
	}
}

func TestAFixThatEndsAfterRecoveryLeavesNoWait(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")  // fix starts
	s.check(t0.Add(32*time.Second), false, "") // it came back by itself meanwhile
	s.done(t0.Add(40*time.Second), nil, false) // the fix answers late
	s.check(t0.Add(50*time.Second), true, "")  // a new problem
	if st := s.check(t0.Add(80*time.Second), true, ""); !st.Start {
		t.Fatalf("new problem after a late fix: %+v, want a new try (no leftover wait)", st)
	}
}

func TestStatus(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	if hs := s.tr.Status("web", s.p, t0.Add(31*time.Second)); !hs.Healing || hs.AttemptsUsed != 1 || hs.Attempts != 3 ||
		hs.Mode != config.HealAuto || hs.What != "restart nginx" {
		t.Fatalf("while fixing: %+v", hs)
	}
	s.done(t0.Add(35*time.Second), errors.New("boom"), false)
	hs := s.tr.Status("web", s.p, t0.Add(36*time.Second))
	if hs.Healing || hs.LastResult != "Tried to restart nginx (attempt 0): boom" || hs.LastAt.IsZero() {
		t.Fatalf("after a failed fix: %+v", hs)
	}
}

func TestPlanFor(t *testing.T) {
	m := config.Monitor{ID: "web", Type: "service", Service: "nginx", For: config.Duration(2 * time.Minute),
		Heal: &config.Heal{Steps: []config.HealStep{{RestartService: []string{"nginx"}}}}}
	p, ok := PlanFor(m)
	if !ok || p.After != 2*time.Minute || p.Attempts != 3 || p.Verify != 2*time.Minute || p.What != "restart nginx" || p.Mode != config.HealAsk {
		t.Fatalf("plan = %+v", p)
	}
	m.For = 0
	if p, _ := PlanFor(m); p.After != MinDelay {
		t.Fatalf("for 0: after = %v, want the minimum %v", p.After, MinDelay)
	}
	off := false
	m.Enabled = &off
	if _, ok := PlanFor(m); ok {
		t.Fatal("a monitor that is off has no plan")
	}
}

// A reminder that is due while a fix runs, after the alert went out, is sent.
func TestAReminderDuringAFixIsNotSwallowed(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.done(t0.Add(31*time.Second), nil, false)
	s.check(t0.Add(3*time.Minute+31*time.Second), true, "") // second try: the alert goes out now
	remind := &engine.Event{Kind: engine.EventFiring, Monitor: "web", Detail: "Still failing after the silence ended. stopped", At: t0.Add(4 * time.Minute)}
	st := s.tr.Step("web", s.p, t0.Add(4*time.Minute), s.eng.Get("web"), remind, "")
	if st.Send != remind || !strings.Contains(st.Note, "pulse is trying to restart nginx") {
		t.Fatalf("reminder while fixing: %+v", st)
	}
}

// A flapping notice is not replaced by the held failure it describes.
func TestAFlappingNoticeWinsOverAHeldFailure(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.check(t0, true, "") // held
	flap := &engine.Event{Kind: engine.EventFlapping, Monitor: "web", Detail: "It changed between failing and recovered 5 times", At: t0.Add(5 * time.Second)}
	st := s.tr.Step("web", s.p, t0.Add(5*time.Second), s.eng.Get("web"), flap, "it keeps switching between failing and working")
	if st.Send != flap || st.Start {
		t.Fatalf("flapping: %+v, want the flapping notice and no fix", st)
	}
}

// A recovery the engine did not announce (it was flapping) still ends the problem,
// so the next problem starts clean instead of reusing the old held alert.
func TestAnUnannouncedRecoveryEndsTheProblem(t *testing.T) {
	s := newSim(t, config.HealAuto)
	s.check(t0, true, "") // held, detail "stopped"
	ok := engine.State{Status: engine.OK, LastChecked: t0.Add(10 * time.Second)}
	s.tr.Step("web", s.p, t0.Add(10*time.Second), ok, nil, "")
	if got := s.tr.Take("web"); got != nil {
		t.Fatalf("the old held alert survived the recovery: %+v", got)
	}
}

// When a heal rule is removed while its alert is held, the alert is handed back.
func TestTakeReturnsAHeldAlert(t *testing.T) {
	s := newSim(t, config.HealAsk)
	s.check(t0, true, "")
	if got := s.tr.Take("web"); got == nil || got.Kind != engine.EventFiring {
		t.Fatalf("take = %+v, want the held firing alert", got)
	}
	if got := s.tr.Take("web"); got != nil {
		t.Fatal("take must forget the monitor")
	}
}

// A problem that started before pulse did (pulse restarted) was already reported: a
// fix that works then says what pulse did, not "it failed ..." out of the blue.
func TestAProblemFromBeforeARestartIsNotRetold(t *testing.T) {
	tr := NewTracker(10)
	p := plan(config.HealAuto)
	firing := engine.State{Status: engine.Firing, ProblemSince: t0.Add(-time.Hour), Detail: "stopped"}
	if st := tr.Step("web", p, t0, firing, nil, ""); !st.Start {
		t.Fatalf("after a restart: %+v, want a try", st)
	}
	tr.Done("web", Outcome{At: t0.Add(time.Second)}, p.Verify)
	resolved := &engine.Event{Kind: engine.EventResolved, Monitor: "web", Detail: "running", At: t0.Add(10 * time.Second)}
	st := tr.Step("web", p, t0.Add(10*time.Second), engine.State{Status: engine.OK}, resolved, "")
	if st.Send != resolved || strings.Contains(st.Note, "It failed") || !strings.Contains(st.Note, "pulse restarted nginx at") {
		t.Fatalf("resolved: %+v", st)
	}
}

// A monitor that recovers long after a fix that did not help is not credited to the fix.
func TestALateRecoveryIsNotCalledFixed(t *testing.T) {
	s := newSim(t, config.HealAsk)
	s.check(t0, true, "")
	s.check(t0.Add(30*time.Second), true, "")
	s.tr.Approve("web", "")
	s.check(t0.Add(31*time.Second), true, "")
	s.done(t0.Add(32*time.Second), nil, true)
	if st := s.check(t0.Add(30*time.Minute), false, ""); st.Kind == alert.KindHealed {
		t.Fatalf("recovered 30 minutes later: %+v, want an ordinary resolved message", st)
	}
}
