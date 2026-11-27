// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package trends

import (
	"math"
	"sort"
	"time"
)

// Forecast says when a disk will be full at its recent rate of growth.
type Forecast struct {
	Disk         string    `json:"disk"`
	FullAt       time.Time `json:"full_at"`
	DaysLeft     float64   `json:"days_left"`
	GrowthPerDay uint64    `json:"growth_per_day"` // bytes
	FreeBytes    uint64    `json:"free"`
	Confidence   float64   `json:"confidence"` // 0 to 1: how steadily the disk has grown (R squared)
	BasedOnDays  float64   `json:"based_on_days"`
}

// What makes a forecast worth saying: enough history, steady growth, and a date close enough
// to act on. Anything less is left out rather than guessed.
const (
	forecastWindow   = 7 * 24 * time.Hour
	minHistory       = 24 * time.Hour
	minPoints        = 12
	minConfidence    = 0.6
	maxDaysAhead     = 90
	cleanupDropShare = 0.02 // a fall of 2% of the disk between two points is a cleanup: start after it
)

// DiskForecasts looks at the last week of `pts` (oldest first) and returns, soonest first, the
// disks that will be full within 90 days at their steady rate of growth.
func DiskForecasts(pts []Point, now time.Time) []Forecast {
	type sample struct {
		at                time.Time
		used, free, total uint64
	}
	byDisk := map[string][]sample{}
	for _, p := range pts {
		if p.At.Before(now.Add(-forecastWindow)) {
			continue
		}
		for path, d := range p.Disks {
			byDisk[path] = append(byDisk[path], sample{p.At, d.UsedBytes, d.FreeBytes, d.TotalBytes})
		}
	}
	var out []Forecast
	for path, s := range byDisk {
		// Start after the last cleanup: a trend from before it says nothing about now.
		start := 0
		for i := 1; i < len(s); i++ {
			if s[i].used < s[i-1].used && float64(s[i-1].used-s[i].used) > cleanupDropShare*float64(s[i].total) {
				start = i
			}
		}
		s = s[start:]
		if len(s) < minPoints || s[len(s)-1].at.Sub(s[0].at) < minHistory {
			continue
		}
		xs, ys := make([]float64, len(s)), make([]float64, len(s))
		for i, v := range s {
			xs[i] = v.at.Sub(s[0].at).Seconds()
			ys[i] = float64(v.used)
		}
		slope, r2 := linearFit(xs, ys)
		perDay := slope * 86400
		last := s[len(s)-1]
		if perDay <= 0 || r2 < minConfidence || last.free == 0 {
			continue
		}
		days := float64(last.free) / perDay
		if days > maxDaysAhead {
			continue
		}
		out = append(out, Forecast{
			Disk: path, FullAt: last.at.Add(time.Duration(days * 24 * float64(time.Hour))).Truncate(time.Hour),
			DaysLeft: math.Round(days*10) / 10, GrowthPerDay: uint64(perDay), FreeBytes: last.free,
			Confidence: math.Round(r2*100) / 100, BasedOnDays: math.Round(last.at.Sub(s[0].at).Hours()/24*10) / 10,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DaysLeft < out[j].DaysLeft })
	return out
}

// linearFit is the least-squares line through the points: its slope and R squared.
func linearFit(xs, ys []float64) (slope, r2 float64) {
	n := float64(len(xs))
	var sx, sy, sxx, sxy, syy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
		syy += ys[i] * ys[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, 0
	}
	slope = (n*sxy - sx*sy) / den
	vy := n*syy - sy*sy
	if vy <= 0 {
		return slope, 0
	}
	r := (n*sxy - sx*sy) / math.Sqrt(den*vy)
	return slope, r * r
}
