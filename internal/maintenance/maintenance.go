// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package maintenance decides whether a weekly maintenance window is open.
package maintenance

import (
	"fmt"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
)

var weekdays = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday,
	"wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}

// Window is one compiled maintenance window.
type Window struct {
	Name     string
	days     [7]bool
	from, to clock
	loc      *time.Location
	monitors map[string]bool // nil means every monitor
}

type clock struct{ h, m int }

// Compile checks and prepares the configured windows.
func Compile(list []config.Maintenance) ([]Window, error) {
	out := make([]Window, 0, len(list))
	for _, m := range list {
		w := Window{Name: m.Name, loc: time.Local}
		for _, d := range m.Days {
			wd, ok := weekdays[d]
			if !ok {
				return nil, fmt.Errorf("maintenance %s: unknown day %q", m.Name, d)
			}
			w.days[wd] = true
		}
		var err error
		if w.from, err = parseClock(m.From); err != nil {
			return nil, fmt.Errorf("maintenance %s: from: %w", m.Name, err)
		}
		if w.to, err = parseClock(m.To); err != nil {
			return nil, fmt.Errorf("maintenance %s: to: %w", m.Name, err)
		}
		if w.from == w.to {
			return nil, fmt.Errorf("maintenance %s: from and to are the same time", m.Name)
		}
		if m.Timezone != "" {
			if w.loc, err = time.LoadLocation(m.Timezone); err != nil {
				return nil, fmt.Errorf("maintenance %s: unknown time zone %q", m.Name, m.Timezone)
			}
		}
		if len(m.Monitors) > 0 {
			w.monitors = map[string]bool{}
			for _, id := range m.Monitors {
				w.monitors[id] = true
			}
		}
		out = append(out, w)
	}
	return out, nil
}

func parseClock(s string) (clock, error) {
	var c clock
	if _, err := fmt.Sscanf(s, "%d:%d", &c.h, &c.m); err != nil || c.h < 0 || c.h > 23 || c.m < 0 || c.m > 59 || len(s) != 5 {
		return clock{}, fmt.Errorf("%q is not a time like 02:30", s)
	}
	return c, nil
}

// Covers reports whether the window is open for monitor at t, and when it closes.
// A window whose end is earlier than its start runs past midnight and belongs
// to the day it starts on.
func (w Window) Covers(monitor string, t time.Time) (time.Time, bool) {
	if w.monitors != nil && !w.monitors[monitor] {
		return time.Time{}, false
	}
	local := t.In(w.loc)
	for back := 0; back <= 1; back++ {
		day := local.AddDate(0, 0, -back)
		if !w.days[day.Weekday()] {
			continue
		}
		y, mo, d := day.Date()
		start := time.Date(y, mo, d, w.from.h, w.from.m, 0, 0, w.loc)
		endDay := d
		if w.to.h*60+w.to.m <= w.from.h*60+w.from.m {
			endDay++
		}
		end := time.Date(y, mo, endDay, w.to.h, w.to.m, 0, 0, w.loc)
		if !t.Before(start) && t.Before(end) {
			return end, true
		}
	}
	return time.Time{}, false
}

// Covering returns the open window for monitor at t that closes last.
func Covering(ws []Window, monitor string, t time.Time) (Window, time.Time, bool) {
	var best Window
	var until time.Time
	found := false
	for _, w := range ws {
		if end, ok := w.Covers(monitor, t); ok && (!found || end.After(until)) {
			best, until, found = w, end, true
		}
	}
	return best, until, found
}
