// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type step struct {
	at    time.Duration
	bad   bool
	clear bool
	want  EventKind // "" means no event
	state Status
}

func run(t *testing.T, r Rule, steps []step) {
	t.Helper()
	e := New(nil)
	for i, s := range steps {
		ev := e.Observe("m", r, t0.Add(s.at), Observation{Bad: s.bad, Clear: s.clear})
		got := EventKind("")
		if ev != nil {
			got = ev.Kind
		}
		if got != s.want {
			t.Fatalf("step %d (t=%v): event %q, want %q", i, s.at, got, s.want)
		}
		if st := e.Snapshot()["m"].Status; st != s.state {
			t.Fatalf("step %d (t=%v): state %q, want %q", i, s.at, st, s.state)
		}
	}
}

func TestShortBlipNeverAlerts(t *testing.T) {
	run(t, Rule{For: time.Minute, Cooldown: time.Hour}, []step{
		{at: 0, bad: true, state: Pending},
		{at: 30 * time.Second, bad: true, state: Pending},
		{at: 45 * time.Second, bad: false, state: OK},
		{at: 60 * time.Second, bad: true, state: Pending},
	})
}

func TestFiresAfterForAndResolves(t *testing.T) {
	run(t, Rule{For: time.Minute, Cooldown: time.Hour}, []step{
		{at: 0, bad: true, state: Pending},
		{at: 60 * time.Second, bad: true, want: EventFiring, state: Firing},
		{at: 90 * time.Second, bad: true, state: Firing},
		{at: 2 * time.Minute, bad: false, want: EventResolved, state: OK},
	})
}

func TestNoForFiresImmediately(t *testing.T) {
	run(t, Rule{Cooldown: time.Hour}, []step{
		{at: 0, bad: true, want: EventFiring, state: Firing},
	})
}

func TestCooldownRepeats(t *testing.T) {
	run(t, Rule{Cooldown: 10 * time.Minute}, []step{
		{at: 0, bad: true, want: EventFiring, state: Firing},
		{at: 5 * time.Minute, bad: true, state: Firing},
		{at: 10 * time.Minute, bad: true, want: EventRepeat, state: Firing},
		{at: 15 * time.Minute, bad: true, state: Firing},
		{at: 20 * time.Minute, bad: true, want: EventRepeat, state: Firing},
	})
}

func TestHysteresisHoldsUntilClear(t *testing.T) {
	// alert > 85, clear < 80: a reading of 82 is neither.
	run(t, Rule{HasClear: true, Cooldown: time.Hour}, []step{
		{at: 0, bad: true, want: EventFiring, state: Firing},
		{at: time.Minute, bad: false, clear: false, state: Firing},
		{at: 2 * time.Minute, bad: true, state: Firing},
		{at: 3 * time.Minute, bad: false, clear: true, want: EventResolved, state: OK},
	})
}

func TestResolvedDurationAndSince(t *testing.T) {
	e := New(nil)
	r := Rule{For: time.Minute, Cooldown: time.Hour}
	e.Observe("m", r, t0, Observation{Bad: true})
	e.Observe("m", r, t0.Add(time.Minute), Observation{Bad: true})
	ev := e.Observe("m", r, t0.Add(14*time.Minute), Observation{})
	if ev == nil || ev.Kind != EventResolved || ev.Duration != 14*time.Minute || !ev.Since.Equal(t0) {
		t.Fatalf("resolved event = %+v", ev)
	}
}

func TestRestartKeepsFiringWithoutResending(t *testing.T) {
	e := New(map[string]State{"m": {Status: Firing, ProblemSince: t0, LastNotified: t0}})
	if ev := e.Observe("m", Rule{Cooldown: time.Hour}, t0.Add(time.Minute), Observation{Bad: true}); ev != nil {
		t.Fatalf("restart re-sent %+v", ev)
	}
}

func TestRetain(t *testing.T) {
	e := New(map[string]State{"a": {Status: OK}, "b": {Status: OK}})
	e.Retain(map[string]bool{"a": true})
	if _, ok := e.Snapshot()["b"]; ok {
		t.Fatal("b should be dropped")
	}
}

func TestRemind(t *testing.T) {
	e := New(nil)
	now := time.Now()
	r := Rule{Cooldown: time.Hour}
	if e.Remind("m", now) != nil {
		t.Fatal("unknown monitor must not remind")
	}
	e.Observe("m", r, now, Observation{Bad: true, Detail: "down"})
	ev := e.Remind("m", now.Add(time.Minute))
	if ev == nil || ev.Kind != EventFiring || ev.Detail != "down" || !ev.Since.Equal(now) {
		t.Fatalf("Remind = %+v", ev)
	}
	if e.Observe("m", r, now.Add(2*time.Minute), Observation{Bad: true}) != nil {
		t.Fatal("a reminder restarts the cooldown")
	}
	e.Observe("m", r, now.Add(3*time.Minute), Observation{})
	if e.Remind("m", now.Add(4*time.Minute)) != nil {
		t.Fatal("a recovered monitor must not remind")
	}
}

func TestFlapping(t *testing.T) {
	e := New(nil)
	r := Rule{Cooldown: time.Hour, FlapChanges: 4, FlapWindow: 10 * time.Minute}
	now := time.Now()
	var kinds []EventKind
	step := func(bad bool) {
		now = now.Add(30 * time.Second)
		if ev := e.Observe("m", r, now, Observation{Bad: bad, Detail: "d"}); ev != nil {
			kinds = append(kinds, ev.Kind)
		}
	}
	for i := 0; i < 6; i++ { // firing, resolved, firing, resolved, then flapping, then quiet
		step(i%2 == 0)
	}
	want := []EventKind{EventFiring, EventResolved, EventFiring, EventFlapping}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	// Still failing and quiet for half the window: one settled event, then normal repeats.
	kinds = nil
	for i := 0; i < 12; i++ {
		step(true)
	}
	if len(kinds) != 1 || kinds[0] != EventSettled {
		t.Fatalf("after settling: %v", kinds)
	}
	if st := e.Snapshot()["m"]; st.Status != Firing || st.Flapping {
		t.Fatalf("state after settling: %+v", st)
	}
}

func TestFlappingOff(t *testing.T) {
	e := New(nil)
	r := Rule{Cooldown: time.Hour}
	now := time.Now()
	n := 0
	for i := 0; i < 20; i++ {
		now = now.Add(time.Second)
		if e.Observe("m", r, now, Observation{Bad: i%2 == 0}) != nil {
			n++
		}
	}
	if n != 20 {
		t.Fatalf("with flapping off every change is sent, got %d of 20", n)
	}
}
