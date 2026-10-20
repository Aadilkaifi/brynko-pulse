// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package engine turns check results into alerts: it waits for a problem to
// last, keeps quiet while it continues, and reports recovery with a margin.
package engine

import (
	"fmt"
	"sync"
	"time"
)

// Status of one monitor.
type Status string

// Monitor statuses.
const (
	Unknown Status = "unknown" // not checked yet
	OK      Status = "ok"
	Pending Status = "pending" // wrong, but not for long enough to alert
	Firing  Status = "firing"
)

// Rule holds the timing of one monitor.
type Rule struct {
	For      time.Duration // how long a problem must last before alerting
	Cooldown time.Duration // how often to repeat while it continues
	HasClear bool          // recovery requires the clear condition, not just "no longer bad"
	// Flapping: this many alerts and recoveries within FlapWindow pause alerts
	// for the monitor until it has been quiet for half the window. 0 turns it off.
	FlapChanges int
	FlapWindow  time.Duration
}

// Observation is the outcome of one check.
type Observation struct {
	Bad    bool   // the alert condition holds, or the check itself failed
	Clear  bool   // the clear condition holds; used only when Rule.HasClear
	Detail string // what was measured, for people
}

// EventKind says what changed.
type EventKind string

// Event kinds.
const (
	EventFiring   EventKind = "firing"
	EventRepeat   EventKind = "repeat"
	EventResolved EventKind = "resolved"
	EventFlapping EventKind = "flapping"
	EventSettled  EventKind = "settled"
)

// Event is something worth telling people about.
type Event struct {
	Kind     EventKind
	Monitor  string
	Detail   string
	Since    time.Time     // when the problem started
	At       time.Time     // when this event happened
	Duration time.Duration // for resolved events: how long the problem lasted
}

// State is what the engine remembers about one monitor.
type State struct {
	Status       Status      `json:"status"`
	ProblemSince time.Time   `json:"problem_since,omitzero"`
	LastChecked  time.Time   `json:"last_checked,omitzero"`
	LastNotified time.Time   `json:"last_notified,omitzero"`
	Detail       string      `json:"detail,omitempty"`
	Changes      []time.Time `json:"changes,omitempty"` // recent alerts and recoveries, for flapping
	Flapping     bool        `json:"flapping,omitempty"`
}

// Engine is safe for concurrent use.
type Engine struct {
	mu     sync.Mutex
	states map[string]State
}

// New starts an engine, optionally from saved state so a restart does not
// re-send alerts that are already out.
func New(saved map[string]State) *Engine {
	states := make(map[string]State, len(saved))
	for k, v := range saved {
		states[k] = v
	}
	return &Engine{states: states}
}

// Observe records a check result and returns an event when people should hear about it.
func (e *Engine) Observe(id string, r Rule, now time.Time, o Observation) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	st, ok := e.states[id]
	if !ok {
		st.Status = Unknown
	}
	ev := observe(&st, id, r, now, o)
	ev = flap(&st, id, r, now, ev)
	e.states[id] = st
	return ev
}

// flap turns a run of alerts and recoveries into one flapping event, holds
// back the rest, and reports once the monitor has settled.
func flap(st *State, id string, r Rule, now time.Time, ev *Event) *Event {
	if r.FlapChanges <= 0 || r.FlapWindow <= 0 {
		st.Changes, st.Flapping = nil, false
		return ev
	}
	// A new slice every time: snapshots handed out earlier still share the old one.
	cut := now.Add(-r.FlapWindow)
	keep := make([]time.Time, 0, len(st.Changes)+1)
	for _, t := range st.Changes {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if ev != nil && (ev.Kind == EventFiring || ev.Kind == EventResolved) {
		keep = append(keep, now)
	}
	st.Changes = keep
	if len(st.Changes) > r.FlapChanges {
		st.Changes = st.Changes[len(st.Changes)-r.FlapChanges:]
	}

	switch {
	case !st.Flapping && len(st.Changes) >= r.FlapChanges:
		st.Flapping = true
		return &Event{Kind: EventFlapping, Monitor: id, At: now, Since: st.Changes[0],
			Detail: fmt.Sprintf("It changed between failing and recovered %d times in %s, so its alerts are paused until it settles. Now: %s",
				len(st.Changes), human(r.FlapWindow), st.Detail)}
	case st.Flapping && (len(st.Changes) == 0 || now.Sub(st.Changes[len(st.Changes)-1]) >= r.FlapWindow/2):
		st.Flapping, st.Changes = false, nil
		state := "It is working."
		if st.Status == Firing {
			state = "It is still failing."
			st.LastNotified = now
		}
		return &Event{Kind: EventSettled, Monitor: id, At: now, Since: st.ProblemSince,
			Detail: "It stopped flapping. " + state + " Now: " + st.Detail}
	case st.Flapping:
		return nil
	}
	return ev
}

func human(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func observe(st *State, id string, r Rule, now time.Time, o Observation) *Event {
	st.LastChecked = now
	st.Detail = o.Detail

	if o.Bad {
		switch st.Status {
		case Unknown, OK:
			st.Status, st.ProblemSince = Pending, now
			if r.For > 0 {
				return nil
			}
			fallthrough
		case Pending:
			if now.Sub(st.ProblemSince) < r.For {
				return nil
			}
			st.Status, st.LastNotified = Firing, now
			return &Event{Kind: EventFiring, Monitor: id, Detail: o.Detail, Since: st.ProblemSince, At: now}
		case Firing:
			if now.Sub(st.LastNotified) < r.Cooldown {
				return nil
			}
			st.LastNotified = now
			return &Event{Kind: EventRepeat, Monitor: id, Detail: o.Detail, Since: st.ProblemSince, At: now}
		}
		return nil
	}

	switch st.Status {
	case Firing:
		if r.HasClear && !o.Clear {
			return nil // between the alert and clear thresholds: still a problem
		}
		ev := &Event{Kind: EventResolved, Monitor: id, Detail: o.Detail, Since: st.ProblemSince, At: now,
			Duration: now.Sub(st.ProblemSince)}
		*st = State{Status: OK, LastChecked: now, Detail: o.Detail, Changes: st.Changes, Flapping: st.Flapping}
		return ev
	default:
		st.Status, st.ProblemSince = OK, time.Time{}
		return nil
	}
}

// Remind returns a firing event for a monitor that is still failing and
// restarts its repeat cooldown, for when earlier alerts were held back. It
// returns nil when the monitor is not failing.
func (e *Engine) Remind(id string, now time.Time) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.states[id]
	if !ok || st.Status != Firing || st.Flapping { // a flapping monitor reports when it settles
		return nil
	}
	st.LastNotified = now
	e.states[id] = st
	return &Event{Kind: EventFiring, Monitor: id, Detail: st.Detail, Since: st.ProblemSince, At: now}
}

// Get returns one monitor's state; Unknown when it has not been checked.
func (e *Engine) Get(id string) State {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.states[id]
	if !ok {
		return State{Status: Unknown}
	}
	st.Changes = append([]time.Time(nil), st.Changes...)
	return st
}

// Snapshot returns a copy of every monitor's state.
func (e *Engine) Snapshot() map[string]State {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]State, len(e.states))
	for k, v := range e.states {
		v.Changes = append([]time.Time(nil), v.Changes...)
		out[k] = v
	}
	return out
}

// Retain drops monitors that are no longer configured.
func (e *Engine) Retain(ids map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id := range e.states {
		if !ids[id] {
			delete(e.states, id)
		}
	}
}
