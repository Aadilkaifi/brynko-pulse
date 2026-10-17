// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package heal is self-healing: when a monitor with a heal rule fails, pulse
// tries the fix it was given, a limited number of times, and says what it did.
//
// The Tracker decides; it does no I/O, so every rule here is tested directly.
// The Fixer carries out a fix.
package heal

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/engine"
	"github.com/Aadilkaifi/brynko-pulse/internal/store"
)

// MinDelay is how long a problem must last before pulse tries a fix, whatever
// the monitor's for: a fix never answers a single bad reading. Only tests change it.
var MinDelay = 30 * time.Second

// Plan is one monitor's heal rule, ready to use.
type Plan struct {
	Action   string
	Run      []string
	Timeout  time.Duration
	Attempts int
	Within   time.Duration
	Verify   time.Duration
	After    time.Duration // how long the problem must last first
	What     string        // the fix in words, such as "restart nginx"
	Done     string        // the same, done: "restarted nginx"
}

// PlanFor returns the plan for a monitor, and false when it has no heal rule.
func PlanFor(m config.Monitor) (Plan, bool) {
	if m.Heal == nil || !m.IsEnabled() {
		return Plan{}, false
	}
	h := m.Heal.Effective()
	p := Plan{Action: h.Action, Run: h.Run, Timeout: h.Timeout.D(), Attempts: h.Attempts,
		Within: h.Within.D(), Verify: h.Verify.D(), After: max(m.For.D(), MinDelay)}
	switch {
	case h.Action == config.HealRestart && m.Type == "container":
		p.What, p.Done = "restart the container "+m.Container, "restarted the container "+m.Container
	case h.Action == config.HealRestart:
		p.What, p.Done = "restart "+m.Service, "restarted "+m.Service
	case len(h.Run) > 0:
		name := filepath.Base(h.Run[0])
		p.What, p.Done = "run "+name, "ran "+name
	}
	return p, true
}

// Outcome is the result of one fix.
type Outcome struct {
	At      time.Time
	Attempt int
	Err     error  // the fix itself could not be done
	Fatal   bool   // trying again cannot help, such as a missing right
	Output  string // what a run fix printed, shortened
}

// Step is what to do after one check of a monitor with a heal rule.
type Step struct {
	Send    *engine.Event // the event to pass on now; nil sends nothing
	Kind    string        // when set, the alert kind to send instead of the event's
	Note    string        // what pulse did, for the alert
	Start   bool          // start a fix now
	Attempt int           // its number within the window
}

// Tracker remembers, per monitor, what self-healing has done. Safe for concurrent use.
type Tracker struct {
	mu      sync.Mutex
	perHour int
	global  []time.Time // every fix started on this server, for the hourly cap
	mons    map[string]*state
}

type state struct {
	tries        []time.Time   // fixes started, for the attempts window
	problemTries int           // fixes during the current problem
	running      bool          // a fix is being done
	verifyUntil  time.Time     // after a fix: wait this long for it to work
	held         *engine.Event // the problem's first alert, held back while pulse tries
	sent         bool          // the problem's alert has gone out
	gaveUp       bool
	fatal        bool // this problem's fix cannot work (such as a missing right)
	last         *Outcome
}

// NewTracker allows at most perHour fixes an hour across all monitors.
func NewTracker(perHour int) *Tracker {
	return &Tracker{perHour: perHour, mons: map[string]*state{}}
}

// SetLimit changes the hourly cap, after a reload.
func (t *Tracker) SetLimit(perHour int) {
	t.mu.Lock()
	t.perHour = perHour
	t.mu.Unlock()
}

// Retain forgets monitors that are no longer configured.
func (t *Tracker) Retain(ids map[string]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id := range t.mons {
		if !ids[id] {
			delete(t.mons, id)
		}
	}
}

func (t *Tracker) get(id string) *state {
	s, ok := t.mons[id]
	if !ok {
		s = &state{}
		t.mons[id] = s
	}
	return s
}

// Step decides, after a check, whether to start a fix and what to tell people.
// st is the monitor's state after the check, ev what the engine said, and
// blocked a reason pulse may not fix it now (a maintenance window, flapping).
func (t *Tracker) Step(id string, p Plan, now time.Time, st engine.State, ev *engine.Event, blocked string) Step {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.get(id)

	if st.Status != engine.Firing {
		if ev == nil || ev.Kind != engine.EventResolved {
			if st.Status == engine.OK && (s.held != nil || s.problemTries > 0) {
				s.endProblem() // it works again, unannounced because it was flapping
			}
			return Step{Send: ev}
		}
		defer s.endProblem()
		switch {
		case s.problemTries > 0 && !s.sent:
			// One message for the whole episode: it broke, pulse fixed it.
			first := ev.Detail
			if s.held != nil {
				first = s.held.Detail
			}
			return Step{Send: ev, Kind: alert.KindHealed, Note: fmt.Sprintf("It failed (%s). pulse %s at %s and it works again.",
				first, p.Done, clock(s.lastAt(now)))}
		case s.problemTries > 0:
			return Step{Send: ev, Note: fmt.Sprintf("pulse %s at %s.", p.Done, clock(s.lastAt(now)))}
		case !s.sent:
			return Step{} // over before pulse had to act and before anyone was told
		}
		return Step{Send: ev}
	}

	switch {
	case ev != nil && ev.Kind == engine.EventFiring && !s.sent && s.held == nil:
		s.held = ev // nobody hears about it until pulse has tried
		ev = nil
	case ev == nil && !s.sent && s.held == nil && s.problemTries == 0:
		s.sent = true // failing since before pulse started: its alert went out then
	}
	if s.running || now.Before(s.verifyUntil) {
		if s.sent && ev != nil {
			// Already reported: a reminder (repeat, silence over) still goes out.
			return Step{Send: ev, Note: fmt.Sprintf("pulse is trying to %s.", p.What)}
		}
		return Step{} // a fix is in progress or settling: wait
	}
	if s.gaveUp {
		return Step{Send: ev}
	}
	if blocked != "" {
		return s.release(ev, "pulse did not try to fix it: "+blocked+".")
	}
	if s.fatal {
		s.gaveUp = true
		out := s.release(ev, fmt.Sprintf("pulse could not %s: %v. It stops trying until this works again.", p.What, s.last.Err))
		out.Kind = alert.KindHealFailed
		return out.orNow(id, st, now)
	}
	if now.Sub(st.ProblemSince) < p.After {
		return Step{} // not long enough yet
	}
	s.tries, t.global = prune(s.tries, now, p.Within), prune(t.global, now, time.Hour)
	used := len(s.tries)
	if used >= p.Attempts || len(t.global) >= t.perHour {
		s.gaveUp = true
		why := fmt.Sprintf("pulse tried to %s %d times in %s and it still fails.", p.What, used, human(p.Within))
		if used < p.Attempts {
			why = fmt.Sprintf("pulse did not try to %s: this server reached its limit of %d fixes an hour.", p.What, t.perHour)
		}
		if s.last != nil && s.last.Err != nil {
			why += fmt.Sprintf(" The last try failed: %v.", s.last.Err)
		}
		out := s.release(ev, why+" It stops trying until this works again.")
		out.Kind = alert.KindHealFailed
		return out.orNow(id, st, now)
	}

	s.running = true
	s.problemTries++
	s.tries = append(s.tries, now)
	t.global = append(t.global, now)
	out := Step{Start: true, Attempt: used + 1}
	switch {
	case s.problemTries > 1 && !s.sent && s.held != nil:
		// The first fix did not help: tell people now, and try again.
		out.Send, out.Note = s.held, fmt.Sprintf("pulse %s at %s but it still fails, so it tries again (%d of %d).",
			p.Done, clock(s.lastAt(now)), used+1, p.Attempts)
		s.sent, s.held = true, nil
	case ev != nil:
		out.Send, out.Note = ev, fmt.Sprintf("pulse is trying to %s (%d of %d).", p.What, used+1, p.Attempts)
		s.sent = true
	}
	return out
}

// Take forgets a monitor that no longer has a heal rule and returns its alert if it was
// held back and never sent, so the runner sends it after all.
func (t *Tracker) Take(id string) *engine.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.mons[id]
	if !ok {
		return nil
	}
	delete(t.mons, id)
	if !s.sent && s.held != nil {
		return s.held
	}
	return nil
}

// Done records the result of a fix: pulse then waits verify for the monitor to work.
func (t *Tracker) Done(id string, o Outcome, verify time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.get(id)
	wasRunning := s.running
	s.running, s.last = false, &o
	if !wasRunning {
		return // the problem ended while the fix ran: nothing to wait for
	}
	s.fatal = o.Fatal
	s.verifyUntil = o.At
	if o.Err == nil {
		s.verifyUntil = o.At.Add(verify)
	}
}

// Status is the heal part of a monitor's status.
func (t *Tracker) Status(id string, p Plan, now time.Time) *store.HealStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.get(id)
	hs := &store.HealStatus{Action: p.Action, Attempts: p.Attempts, AttemptsUsed: within(s.tries, now, p.Within),
		Healing: s.running || now.Before(s.verifyUntil), GaveUp: s.gaveUp}
	if s.last != nil {
		hs.LastAt, hs.LastResult = s.last.At, Describe(p, *s.last)
	}
	return hs
}

// Describe says what one fix did, for history and status.
func Describe(p Plan, o Outcome) string {
	if o.Err != nil {
		return fmt.Sprintf("Tried to %s (attempt %d): %v", p.What, o.Attempt, o.Err)
	}
	return fmt.Sprintf("%s (attempt %d)", capitalise(p.Done), o.Attempt)
}

// release sends the held first alert, or else ev, with a note.
func (s *state) release(ev *engine.Event, note string) Step {
	if ev != nil && ev.Kind != engine.EventFiring && ev.Kind != engine.EventRepeat {
		s.sent, s.held = true, nil // such as flapping: it says more than the held failure
		return Step{Send: ev, Note: note}
	}
	if !s.sent && s.held != nil {
		held := s.held
		s.sent, s.held = true, nil
		return Step{Send: held, Note: note}
	}
	if ev != nil {
		s.sent = true
		return Step{Send: ev, Note: note}
	}
	return Step{Note: note} // nothing to send now; a give-up still carries its reason
}

// orNow makes sure a give-up is said even when there was nothing to send.
func (o Step) orNow(id string, st engine.State, now time.Time) Step {
	if o.Send == nil {
		o.Send = &engine.Event{Kind: engine.EventFiring, Monitor: id, Detail: st.Detail, Since: st.ProblemSince, At: now}
	}
	return o
}

func (s *state) endProblem() {
	s.problemTries, s.running, s.held, s.sent, s.gaveUp, s.fatal = 0, false, nil, false, false, false
	s.verifyUntil = time.Time{}
}

func (s *state) lastAt(now time.Time) time.Time {
	if s.last != nil {
		return s.last.At
	}
	return now
}

// prune keeps the times in list after now-window.
func prune(list []time.Time, now time.Time, window time.Duration) []time.Time {
	keep := list[:0]
	for _, at := range list {
		if now.Sub(at) < window {
			keep = append(keep, at)
		}
	}
	return keep
}

// within counts the times in list after now-window.
func within(list []time.Time, now time.Time, window time.Duration) int {
	n := 0
	for _, at := range list {
		if now.Sub(at) < window {
			n++
		}
	}
	return n
}

func clock(t time.Time) string { return t.Local().Format("15:04") }

func human(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
