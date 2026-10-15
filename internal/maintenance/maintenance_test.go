// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package maintenance

import (
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
)

func at(s string, loc *time.Location) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCovers(t *testing.T) {
	kol, _ := time.LoadLocation("Asia/Kolkata")
	ws, err := Compile([]config.Maintenance{
		{Name: "patch", Days: []string{"sun"}, From: "02:00", To: "03:00", Timezone: "Asia/Kolkata"},
		{Name: "night", Days: []string{"fri"}, From: "23:00", To: "01:00", Timezone: "Asia/Kolkata", Monitors: []string{"web"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 2026-10-18 is a Sunday.
	for _, c := range []struct {
		when, monitor string
		open          bool
		until         string
	}{
		{"2026-10-18 02:00", "db", true, "2026-10-18 03:00"},
		{"2026-10-18 02:59", "db", true, "2026-10-18 03:00"},
		{"2026-10-18 03:00", "db", false, ""},
		{"2026-10-18 01:59", "db", false, ""},
		{"2026-10-17 02:30", "db", false, ""}, // Saturday
		{"2026-10-16 23:30", "web", true, "2026-10-17 01:00"},
		{"2026-10-17 00:30", "web", true, "2026-10-17 01:00"}, // past midnight, started Friday
		{"2026-10-17 00:30", "db", false, ""},                 // not in the monitor list
		{"2026-10-18 00:30", "web", false, ""},                // Saturday night: no window
	} {
		_, until, ok := Covering(ws, c.monitor, at(c.when, kol))
		if ok != c.open || (ok && !until.Equal(at(c.until, kol))) {
			t.Errorf("%s %s: open=%v until=%v, want %v %s", c.when, c.monitor, ok, until, c.open, c.until)
		}
	}
	// The same instant seen from another zone gives the same answer.
	if _, _, ok := Covering(ws, "db", at("2026-10-17 20:45", time.UTC)); !ok {
		t.Error("20:45 UTC is 02:15 in Kolkata on Sunday")
	}
}

func TestCompileErrors(t *testing.T) {
	for _, m := range []config.Maintenance{
		{Name: "a", Days: []string{"sunday"}, From: "02:00", To: "03:00"},
		{Name: "b", Days: []string{"sun"}, From: "2:00", To: "03:00"},
		{Name: "c", Days: []string{"sun"}, From: "02:00", To: "02:00"},
		{Name: "d", Days: []string{"sun"}, From: "02:00", To: "03:00", Timezone: "Mars/Base"},
	} {
		if _, err := Compile([]config.Maintenance{m}); err == nil {
			t.Errorf("%+v should fail", m)
		}
	}
}
