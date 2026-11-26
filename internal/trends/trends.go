// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package trends keeps the machine's own readings over time (CPU, memory, swap, load and
// disks), so a person or a program can see how they changed and when a disk will fill.
//
// One reading a minute is kept for a day; 15-minute averages for 14 days; hourly averages
// for 90 days. Each level is a JSON Lines file in the data folder, a few MB in all.
package trends

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/fsutil"
)

const (
	dirPerm      = 0o750
	filePerm     = 0o640
	maxLineBytes = 64 << 10
)

// Level is one resolution of stored readings.
type Level struct {
	Name string        // "1m", "15m" or "1h"
	Step time.Duration // the time one point stands for
	Keep time.Duration // how long points are kept
	file string
}

// Levels, finest first.
var Levels = []Level{
	{Name: "1m", Step: time.Minute, Keep: 24 * time.Hour, file: "minute.jsonl"},
	{Name: "15m", Step: 15 * time.Minute, Keep: 14 * 24 * time.Hour, file: "quarter.jsonl"},
	{Name: "1h", Step: time.Hour, Keep: 90 * 24 * time.Hour, file: "hour.jsonl"},
}

// Disk is one filesystem at one moment, or its average over a point's time.
type Disk struct {
	UsedPct    float64 `json:"used_pct"`
	UsedBytes  uint64  `json:"used"`
	FreeBytes  uint64  `json:"free"`
	TotalBytes uint64  `json:"total"`
}

// Point is a reading, or the average of the readings in its time when Samples is above one.
// A value the system could not give is left out.
type Point struct {
	At      time.Time `json:"at"`
	Samples int       `json:"samples"`

	CPU      *float64 `json:"cpu,omitempty"`     // percent busy
	CPUMax   *float64 `json:"cpu_max,omitempty"` // the highest reading within an average
	Mem      *float64 `json:"mem,omitempty"`     // percent used
	MemMax   *float64 `json:"mem_max,omitempty"`
	MemUsed  uint64   `json:"mem_used,omitempty"`
	MemTotal uint64   `json:"mem_total,omitempty"`
	Swap     *float64 `json:"swap,omitempty"` // percent used; absent with no swap
	Load1    *float64 `json:"load1,omitempty"`
	Load5    *float64 `json:"load5,omitempty"`
	Load15   *float64 `json:"load15,omitempty"`

	Disks map[string]Disk `json:"disks,omitempty"` // by mount path
}

// LevelFor is the finest level that still covers the last `span`.
func LevelFor(span time.Duration) Level {
	for _, l := range Levels {
		if span <= l.Keep {
			return l
		}
	}
	return Levels[len(Levels)-1]
}

// Read returns the points of one level from `since` on, oldest first. A level with no file
// yet has no points.
func Read(dir string, l Level, since time.Time) ([]Point, error) {
	f, err := os.Open(filepath.Join(dir, l.file))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Point
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), maxLineBytes)
	for sc.Scan() {
		var p Point
		if json.Unmarshal(sc.Bytes(), &p) != nil {
			continue // a line cut short by a power cut is skipped, not fatal
		}
		if !p.At.Before(since) {
			out = append(out, p)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", l.file, err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func appendPoint(dir string, l Level, p Point) error {
	line, err := json.Marshal(p)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, l.file), os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// prune rewrites a level's file without the points older than it keeps.
func prune(dir string, l Level, now time.Time) error {
	pts, err := Read(dir, l, now.Add(-l.Keep))
	if err != nil {
		return err
	}
	var buf []byte
	for _, p := range pts {
		line, err := json.Marshal(p)
		if err != nil {
			return err
		}
		buf = append(append(buf, line...), '\n')
	}
	return fsutil.WriteAtomic(filepath.Join(dir, l.file), buf, filePerm)
}

// Average is one point standing for `pts`: the mean of each value, the highest CPU and memory
// reading, and each disk as it was at the end (a disk changes slowly; its last state counts).
func Average(at time.Time, pts []Point) Point {
	out := Point{At: at}
	if len(pts) == 0 {
		return out
	}
	var cpu, mem, swap, l1, l5, l15 mean
	cpuMax, memMax := math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		out.Samples += max(1, p.Samples)
		w := float64(max(1, p.Samples))
		cpu.add(p.CPU, w)
		mem.add(p.Mem, w)
		swap.add(p.Swap, w)
		l1.add(p.Load1, w)
		l5.add(p.Load5, w)
		l15.add(p.Load15, w)
		cpuMax = math.Max(cpuMax, highest(p.CPUMax, p.CPU))
		memMax = math.Max(memMax, highest(p.MemMax, p.Mem))
		if p.MemTotal > 0 {
			out.MemUsed, out.MemTotal = p.MemUsed, p.MemTotal
		}
		if len(p.Disks) > 0 {
			out.Disks = p.Disks
		}
	}
	out.CPU, out.Mem, out.Swap = cpu.value(), mem.value(), swap.value()
	out.Load1, out.Load5, out.Load15 = l1.value(), l5.value(), l15.value()
	if !math.IsInf(cpuMax, -1) {
		out.CPUMax = round1(cpuMax)
	}
	if !math.IsInf(memMax, -1) {
		out.MemMax = round1(memMax)
	}
	return out
}

type mean struct{ sum, weight float64 }

func (m *mean) add(v *float64, w float64) {
	if v != nil {
		m.sum += *v * w
		m.weight += w
	}
}

func (m mean) value() *float64 {
	if m.weight == 0 {
		return nil
	}
	return round1(m.sum / m.weight)
}

func highest(a, b *float64) float64 {
	switch {
	case a != nil:
		return *a
	case b != nil:
		return *b
	}
	return math.Inf(-1)
}

func round1(v float64) *float64 {
	r := math.Round(v*10) / 10
	return &r
}
