// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/duration"
	"github.com/Aadilkaifi/brynko-pulse/internal/trends"
)

const trendsDetails = `pulse records CPU, memory, swap, load and disk use once a minute while it runs: every
reading for a day, 15-minute averages for 14 days, hourly averages for 90 days. --since picks
the span; the finest detail that covers it is shown. A disk that has grown steadily is given
the date it will be full at that rate, when that is within 90 days.`

func cmdTrends(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("trends", stderr)
	dataDir := fs.String("data", platformFor().DataDir(), "where state and alert history are kept")
	sinceText := fs.String("since", "24h", "how far back to look, for example 6h, 7d or 30d (at most 90d)")
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(reorderFlags(args)) != nil {
		return exitUsage
	}
	since, err := duration.Parse(*sinceText)
	if err == nil && (since <= 0 || since > trends.Levels[len(trends.Levels)-1].Keep) {
		err = errors.New("--since must be between 1m and 90d")
	}
	now := time.Now()
	var (
		level     trends.Level
		pts       []trends.Point
		forecasts []trends.Forecast
	)
	if err == nil {
		dir := filepath.Join(*dataDir, "trends")
		level = trends.LevelFor(since)
		pts, err = trends.Read(dir, level, now.Add(-since))
		if err == nil {
			forecasts, err = diskForecasts(dir, now)
		}
	}
	if *asJSON {
		if pts == nil {
			pts = []trends.Point{}
		}
		if forecasts == nil {
			forecasts = []trends.Forecast{}
		}
		return finishJSON(stdout, stderr, struct {
			envelope
			Level     string            `json:"level"`
			Since     time.Time         `json:"since"`
			Points    []trends.Point    `json:"points"`
			Forecasts []trends.Forecast `json:"forecasts"`
		}{newEnvelope("trends", err), level.Name, now.Add(-since).UTC().Truncate(time.Second), pts, forecasts}, err)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	if len(pts) == 0 {
		fmt.Fprintln(stdout, "No readings yet. pulse records one a minute while it runs; check back in a few minutes.")
		return exitOK
	}
	printTrends(stdout, pts, level, *sinceText)
	printForecasts(stdout, forecasts)
	return exitOK
}

// diskForecasts reads the 15-minute averages of the last week, the detail forecasts are made from.
func diskForecasts(dir string, now time.Time) ([]trends.Forecast, error) {
	pts, err := trends.Read(dir, trends.Levels[1], now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	return trends.DiskForecasts(pts, now), nil
}

func printTrends(w io.Writer, pts []trends.Point, level trends.Level, since string) {
	fmt.Fprintf(w, "Last %s (%d points of %s):\n\n", since, len(pts), level.Name)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "\tNOW\tAVERAGE\tHIGHEST")
	row := func(name string, get func(trends.Point) *float64, peak func(trends.Point) *float64, unit string) {
		var sum, n, top float64
		var last *float64
		for _, p := range pts {
			if v := get(p); v != nil {
				sum, n, last = sum+*v, n+1, v
				h := *v
				if peak != nil && peak(p) != nil {
					h = *peak(p)
				}
				top = max(top, h)
			}
		}
		if n > 0 {
			fmt.Fprintf(tw, "%s\t%.1f%s\t%.1f%s\t%.1f%s\n", name, *last, unit, sum/n, unit, top, unit)
		}
	}
	row("CPU", func(p trends.Point) *float64 { return p.CPU }, func(p trends.Point) *float64 { return p.CPUMax }, "%")
	row("Memory", func(p trends.Point) *float64 { return p.Mem }, func(p trends.Point) *float64 { return p.MemMax }, "%")
	row("Swap", func(p trends.Point) *float64 { return p.Swap }, nil, "%")
	row("Load (1 min)", func(p trends.Point) *float64 { return p.Load1 }, nil, "")
	tw.Flush()

	last := pts[len(pts)-1]
	if len(last.Disks) == 0 {
		return
	}
	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DISK\tUSED NOW\tFREE\tCHANGE IN THIS TIME")
	paths := make([]string, 0, len(last.Disks))
	for p := range last.Disks {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		d := last.Disks[path]
		change := "-"
		for _, p := range pts {
			if first, ok := p.Disks[path]; ok {
				change = signedBytes(int64(d.UsedBytes) - int64(first.UsedBytes))
				break
			}
		}
		fmt.Fprintf(tw, "%s\t%.1f%%\t%s\t%s\n", path, d.UsedPct, bytesText(d.FreeBytes), change)
	}
	tw.Flush()
}

func printForecasts(w io.Writer, fc []trends.Forecast) {
	if len(fc) == 0 {
		return
	}
	fmt.Fprintln(w)
	for _, f := range fc {
		fmt.Fprintf(w, "%s fills up in about %s (around %s) at its rate of the last %.0f days: %s a day, %s free.\n",
			f.Disk, daysText(f.DaysLeft), f.FullAt.Local().Format("2 Jan"), f.BasedOnDays,
			bytesText(f.GrowthPerDay), bytesText(f.FreeBytes))
	}
}

func daysText(d float64) string {
	if d < 1 {
		return fmt.Sprintf("%.0f hours", d*24)
	}
	if d < 1.5 {
		return "1 day"
	}
	return fmt.Sprintf("%.0f days", d)
}

func bytesText(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %sB", float64(b)/float64(div), strings.Split("K M G T P", " ")[exp])
}

func signedBytes(d int64) string {
	if d < 0 {
		return "-" + bytesText(uint64(-d))
	}
	return "+" + bytesText(uint64(d))
}
