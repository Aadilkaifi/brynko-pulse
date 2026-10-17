// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package heal is self-healing: when a monitor with a heal rule fails, pulse
// prepares the fix it was given and either waits for a person to approve it
// (ask) or runs it by itself a limited number of times (auto), and says what
// it did.
//
// The Tracker decides; it does no I/O, so every rule here is tested directly.
// The Fixer carries out a fix; requests.go carries approvals from the CLI.
package heal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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

// MinDelay is how long a problem must last before pulse fixes or offers a fix,
// whatever the monitor's for: a fix never answers a single bad reading. Only
// tests change it.
var MinDelay = 30 * time.Second

const (
	// AutoBeforeAsk is how many automatic fixes one problem gets; when they did
	// not help, the next fix waits for approval.
	AutoBeforeAsk = 2
	// WaitFor is how long a prepared fix waits for approval before it is
	// prepared again, so an approval never answers a situation that has changed.
	WaitFor = time.Hour
)

// backoff is the pause after an automatic fix that did not help, before the next.
var backoff = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

// ErrNotWaiting means there is no such fix waiting for approval.
var ErrNotWaiting = errors.New("this fix is no longer waiting: the problem ended, it was dismissed, or a newer fix replaced it")

// Plan is one monitor's heal rule, ready to use.
type Plan struct {
	Mode     string
	Steps    []config.HealStep
	OnlyIfOK []string
	Timeout  time.Duration
	Attempts int
	Within   time.Duration
	Verify   time.Duration
	After    time.Duration // how long the problem must last first
	What     string        // the fix in words, such as "restart php8.3-fpm and nginx"
	Done     string        // the same, done: "restarted php8.3-fpm and nginx"
	Approve  string        // the command that approves it, for alerts
}

// PlanFor returns the plan for a monitor, and false when it has no heal rule in use.
func PlanFor(m config.Monitor) (Plan, bool) {
	if m.Heal == nil || !m.IsEnabled() || len(m.Heal.Steps) == 0 {
		return Plan{}, false
	}
	h := m.Heal.Effective()
	if h.Mode == config.HealOff {
		return Plan{}, false
	}
	p := Plan{Mode: h.Mode, Steps: h.Steps, OnlyIfOK: h.OnlyIfOK, Timeout: h.Timeout.D(), Attempts: h.Attempts,
		Within: h.Within.D(), Verify: h.Verify.D(), After: max(m.For.D(), MinDelay),
		Approve: "sudo pulse fix approve " + m.ID}
	p.What, p.Done = words(h.Steps)
	return p, true
}

// words says the steps as a fix to do and as a fix done.
func words(steps []config.HealStep) (what, done string) {
	var todo, did []string
	for _, s := range steps {
		switch {
		case len(s.RestartService) > 0:
			list := and(s.RestartService)
			todo, did = append(todo, "restart "+list), append(did, "restarted "+list)
		case len(s.RestartContainer) > 0:
			noun := "the container "
			if len(s.RestartContainer) > 1 {
				noun = "the containers "
			}
			list := noun + and(s.RestartContainer)
			todo, did = append(todo, "restart "+list), append(did, "restarted "+list)
		case len(s.Run) > 0:
			name := filepath.Base(strings.ReplaceAll(s.Run[0], `\`, "/"))
			todo, did = append(todo, "run "+name), append(did, "ran "+name)
		}
	}
	return strings.Join(todo, ", then "), strings.Join(did, ", then ")
}

func and(list []string) string {
	if len(list) == 1 {
		return list[0]
	}
	return strings.Join(list[:len(list)-1], ", ") + " and " + list[len(list)-1]
}

// Outcome is the result of one fix.
type Outcome struct {
	At       time.Time
	Attempt  int
	Approved bool   // a person approved it
	Err      error  // the fix itself could not be done
	Fatal    bool   // trying again cannot help, such as a missing right
	Output   string // what a run step printed, shortened
}

// Step is what to do after one check of a monitor with a heal rule.
type Step struct {
	Send     *engine.Event // the event to pass on now; nil sends nothing
	Kind     string        // when set, the alert kind to send instead of the event's
	Note     string        // what pulse did, for the alert
	Start    bool          // start a fix now
	Attempt  int           // its number within this problem
	Approved bool          // the fix to start was approved by a person
	Offered  string        // a fix now waits for approval: its id, for the history
}

// Tracker remembers, per monitor, what self-healing has done. Safe for concurrent use.
type Tracker struct {
	mu      sync.Mutex
	perHour int
	global  []time.Time // automatic fixes started on this server, for the hourly cap
	mons    map[string]*state
	newID   func() string
}

type state struct {
	tries        []time.Time   // automatic fixes started, for the attempts window
	problemTries int           // fixes during the current problem
	autoTries    int           // automatic fixes during the current problem
	running      bool          // a fix is being done
	verifyUntil  time.Time     // after a fix: wait this long for it to work
	nextAt       time.Time     // the earliest next automatic fix
	held         *engine.Event // the problem's first alert, held back until pulse acts
	sent         bool          // the problem's alert has gone out
	gaveUp       bool
	fatal        bool // this problem's fix cannot work (such as a missing right)
	last         *Outcome
	waiting      *store.HealWaiting
	approved     bool // the waiting fix was approved: start it at the next check
	dismissed    bool // a person turned the fix down: no more offers in this problem
}

// NewTracker allows at most perHour automatic fixes an hour across all monitors.
func NewTracker(perHour int) *Tracker {
	return &Tracker{perHour: perHour, mons: map[string]*state{}, newID: randomID}
}

func randomID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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

// Approve marks the fix waiting for monitor id as approved; it starts at the next
// check. fixID must name the fix waiting now, so an approval never runs a fix
// prepared for another moment; empty approves whatever is waiting.
func (t *Tracker) Approve(id, fixID string) (store.HealWaiting, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.mons[id]
	if !ok || s.waiting == nil || (fixID != "" && fixID != s.waiting.ID) {
		return store.HealWaiting{}, ErrNotWaiting
	}
	s.approved = true
	return *s.waiting, nil
}

// Dismiss turns down the fix waiting for monitor id: no other fix is offered
// until the problem ends.
func (t *Tracker) Dismiss(id, fixID string) (store.HealWaiting, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.mons[id]
	if !ok || s.waiting == nil || (fixID != "" && fixID != s.waiting.ID) {
		return store.HealWaiting{}, ErrNotWaiting
	}
	w := *s.waiting
	s.waiting, s.approved, s.dismissed = nil, false, true
	return w, nil
}

// Step decides, after a check, whether to start or offer a fix and what to tell
// people. st is the monitor's state after the check, ev what the engine said, and
// blocked a reason pulse may not fix it by itself now (a maintenance window,
// flapping, a monitor in only_if_ok failing). A fix a person approved is not blocked.
func (t *Tracker) Step(id string, p Plan, now time.Time, st engine.State, ev *engine.Event, blocked string) Step {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.get(id)

	if st.Status != engine.Firing {
		return s.settle(p, now, st, ev)
	}

	switch {
	case ev != nil && ev.Kind == engine.EventFiring && !s.sent && s.held == nil:
		s.held = ev // held until pulse fixes it or offers a fix, so one alert says both
		ev = nil
	case ev == nil && !s.sent && s.held == nil && s.problemTries == 0 && s.waiting == nil:
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
	if s.fatal {
		s.gaveUp, s.waiting, s.approved = true, nil, false
		out := s.release(ev, fmt.Sprintf("pulse could not %s: %v. It stops trying until this works again.", p.What, s.last.Err))
		out.Kind = alert.KindHealFailed
		return out.orNow(id, st, now)
	}
	if s.approved && s.waiting != nil {
		s.waiting, s.approved = nil, false
		s.running = true
		s.problemTries++
		return Step{Send: ev, Start: true, Attempt: s.problemTries, Approved: true,
			Note: fmt.Sprintf("pulse is doing the fix you approved: %s.", p.What)}
	}
	if blocked != "" {
		return s.release(ev, "pulse did not try to fix it: "+blocked+".")
	}
	if now.Sub(st.ProblemSince) < p.After {
		return Step{} // not long enough yet
	}
	if s.waiting != nil {
		if !now.Before(s.waiting.Expires) {
			// Prepared afresh: an approval names the fix prepared for this moment.
			s.waiting = &store.HealWaiting{ID: t.newID(), Since: now, Expires: now.Add(WaitFor), Why: s.waiting.Why}
		}
		return s.pass(ev, p.waitingNote(""))
	}
	if s.dismissed {
		return s.pass(ev, "")
	}

	why := ""
	if p.Mode == config.HealAuto {
		s.tries, t.global = prune(s.tries, now, p.Within), prune(t.global, now, time.Hour)
		used := len(s.tries)
		switch {
		case s.autoTries >= AutoBeforeAsk:
			why = fmt.Sprintf("pulse %s %d times and it still fails, so the next fix waits for your approval.", p.Done, s.autoTries)
		case used >= p.Attempts:
			why = fmt.Sprintf("pulse used the %d automatic tries allowed in %s, so the next fix waits for your approval.", used, human(p.Within))
		case len(t.global) >= t.perHour:
			why = fmt.Sprintf("this server reached its limit of %d automatic fixes an hour, so this one waits for your approval.", t.perHour)
		case now.Before(s.nextAt):
			return s.pass(ev, "")
		default:
			return t.startAuto(s, p, now, ev, used)
		}
	}
	if s.problemTries > 0 && why == "" {
		why = fmt.Sprintf("pulse did the fix (%s) but it still fails.", p.Done)
	}
	s.waiting = &store.HealWaiting{ID: t.newID(), Since: now, Expires: now.Add(WaitFor), Why: why}
	out := s.release(ev, p.waitingNote(why))
	out.Offered = s.waiting.ID
	if s.problemTries > 0 {
		out.Kind = alert.KindHealFailed
	}
	return out.orNow(id, st, now)
}

// startAuto starts an automatic fix and says so when people should hear it now.
func (t *Tracker) startAuto(s *state, p Plan, now time.Time, ev *engine.Event, used int) Step {
	s.running = true
	s.problemTries++
	s.autoTries++
	s.tries = append(s.tries, now)
	t.global = append(t.global, now)
	out := Step{Start: true, Attempt: s.problemTries}
	switch {
	case s.autoTries > 1 && !s.sent && s.held != nil:
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

// settle handles a check that is not failing: the end of a problem, with what pulse did about it.
func (s *state) settle(p Plan, now time.Time, st engine.State, ev *engine.Event) Step {
	if ev == nil || ev.Kind != engine.EventResolved {
		if st.Status == engine.OK && (s.held != nil || s.problemTries > 0 || s.waiting != nil) {
			s.endProblem() // it works again, unannounced because it was flapping or never reported
		}
		return Step{Send: ev}
	}
	defer s.endProblem()
	// Credited to the fix only when it worked and the monitor recovered soon after it.
	fixed := s.last != nil && s.last.Err == nil && s.problemTries > 0 && !now.After(s.verifyUntil.Add(p.Verify))
	by := ""
	if s.last != nil && s.last.Approved {
		by = " after your approval"
	}
	switch {
	case fixed && !s.sent:
		// One message for the whole episode: it broke, pulse fixed it.
		first := ev.Detail
		if s.held != nil {
			first = s.held.Detail
		}
		return Step{Send: ev, Kind: alert.KindHealed, Note: fmt.Sprintf("It failed (%s). pulse %s at %s%s and it works again.",
			first, p.Done, clock(s.lastAt(now)), by)}
	case fixed:
		return Step{Send: ev, Kind: alert.KindHealed, Note: fmt.Sprintf("pulse %s at %s%s and it works again.",
			p.Done, clock(s.lastAt(now)), by)}
	case s.waiting != nil && s.sent:
		return Step{Send: ev, Note: "It works again by itself; the fix that was waiting is no longer needed."}
	case s.problemTries > 0 && s.sent:
		return Step{Send: ev, Note: fmt.Sprintf("pulse %s at %s.", p.Done, clock(s.lastAt(now)))}
	case !s.sent:
		return Step{} // over before pulse acted and before anyone was told
	}
	return Step{Send: ev}
}

func (p Plan) waitingNote(why string) string {
	note := fmt.Sprintf("A fix is waiting for your approval: %s. Approve it with %s, or from brynko devOps Agent.", p.What, p.Approve)
	if why != "" {
		return why + " " + note
	}
	return note
}

// pass sends a reminder or a late event that is due anyway, with a note.
func (s *state) pass(ev *engine.Event, note string) Step {
	if ev != nil && s.sent {
		return Step{Send: ev, Note: note}
	}
	return Step{}
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
	if !o.Approved && s.autoTries > 0 {
		s.nextAt = s.verifyUntil.Add(backoff[min(s.autoTries, len(backoff))-1])
	}
}

// Status is the heal part of a monitor's status.
func (t *Tracker) Status(id string, p Plan, now time.Time) *store.HealStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.get(id)
	hs := &store.HealStatus{Mode: p.Mode, What: p.What, Attempts: p.Attempts, AttemptsUsed: within(s.tries, now, p.Within),
		Healing: s.running || now.Before(s.verifyUntil), GaveUp: s.gaveUp, Dismissed: s.dismissed}
	if s.waiting != nil {
		w := *s.waiting
		hs.Waiting, hs.Approved = &w, s.approved
	}
	if s.last != nil {
		hs.LastAt, hs.LastResult = s.last.At, Describe(p, *s.last)
	}
	return hs
}

// Describe says what one fix did, for history and status.
func Describe(p Plan, o Outcome) string {
	by := ""
	if o.Approved {
		by = ", approved"
	}
	if o.Err != nil {
		return fmt.Sprintf("Tried to %s (attempt %d%s): %v", p.What, o.Attempt, by, o.Err)
	}
	return fmt.Sprintf("%s (attempt %d%s)", capitalise(p.Done), o.Attempt, by)
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
	return Step{Note: note} // nothing to send now; the caller decides whether it must be said
}

// orNow makes sure the message is said even when there was nothing to send.
func (o Step) orNow(id string, st engine.State, now time.Time) Step {
	if o.Send == nil {
		o.Send = &engine.Event{Kind: engine.EventFiring, Monitor: id, Detail: st.Detail, Since: st.ProblemSince, At: now}
	}
	return o
}

func (s *state) endProblem() {
	s.problemTries, s.autoTries, s.running, s.held, s.sent, s.gaveUp, s.fatal = 0, 0, false, nil, false, false, false
	s.verifyUntil, s.nextAt = time.Time{}, time.Time{}
	s.waiting, s.approved, s.dismissed = nil, false, false
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
