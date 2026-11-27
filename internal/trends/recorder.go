// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package trends

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// Source is what the recorder reads: the platform's own measurements.
type Source interface {
	DiskUsage(path string) (host.Disk, error)
	Memory() (host.Memory, error)
	CPUTimes() (host.CPUTimes, error)
	LoadAverage() (host.Load, error)
}

// Recorder takes one reading a minute and keeps the averages up to date.
type Recorder struct {
	dir   string
	src   Source
	disks func() []string // the mount paths to record, asked each minute (the configuration can change)
	log   *slog.Logger

	mu      sync.Mutex
	prevCPU *host.CPUTimes
	pending map[string][]Point // finer points not yet averaged, by the level they will average into
	pruned  time.Time
}

// NewRecorder records into dir, creating it when needed.
func NewRecorder(dir string, src Source, disks func() []string, log *slog.Logger) (*Recorder, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("create trends folder %s: %w", dir, err)
	}
	r := &Recorder{dir: dir, src: src, disks: disks, log: log, pending: map[string][]Point{}}
	r.resume(time.Now())
	return r, nil
}

// Run records until ctx ends, at the start of each minute.
func (r *Recorder) Run(ctx context.Context) {
	for {
		now := time.Now()
		wait := now.Truncate(time.Minute).Add(time.Minute).Sub(now)
		select {
		case <-ctx.Done():
			return
		case at := <-time.After(wait):
			if err := r.Record(at); err != nil {
				r.log.Warn("trends: reading not recorded", "error", err.Error())
			}
		}
	}
}

// Record takes one reading at `at`, stores it, and stores any average whose time has ended.
func (r *Recorder) Record(at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.read(at.UTC().Truncate(time.Minute))
	if err := appendPoint(r.dir, Levels[0], p); err != nil {
		return err
	}
	if err := r.roll(p); err != nil {
		return err
	}
	if at.Sub(r.pruned) >= time.Hour {
		for _, l := range Levels {
			if err := prune(r.dir, l, at); err != nil {
				return err
			}
		}
		r.pruned = at
	}
	return nil
}

// roll adds a finer point to the next level's pending list and writes that level's average
// once its time has ended, then does the same one level up.
func (r *Recorder) roll(p Point) error {
	for i := 1; i < len(Levels); i++ {
		l := Levels[i]
		list := r.pending[l.Name]
		if n := len(list); n > 0 && !list[n-1].At.Truncate(l.Step).Equal(p.At.Truncate(l.Step)) {
			avg := Average(list[n-1].At.Truncate(l.Step), list)
			if err := appendPoint(r.dir, l, avg); err != nil {
				return err
			}
			r.pending[l.Name] = []Point{p}
			p = avg // the finished average feeds the level above
			continue
		}
		r.pending[l.Name] = append(list, p)
		return nil
	}
	return nil
}

// resume reloads the readings of the averages still open, so a restart does not lose them.
func (r *Recorder) resume(now time.Time) {
	minute, err := Read(r.dir, Levels[0], now.Truncate(time.Hour))
	if err != nil {
		r.log.Warn("trends: earlier readings not reloaded", "error", err.Error())
		return
	}
	quarter, _ := Read(r.dir, Levels[1], now.Truncate(time.Hour))
	q := Levels[1]
	for _, p := range minute {
		if p.At.Truncate(q.Step).Equal(now.Truncate(q.Step)) {
			r.pending[q.Name] = append(r.pending[q.Name], p)
		}
	}
	r.pending[Levels[2].Name] = quarter
	r.pruned = now
}

// read takes one reading. A value the system cannot give is left out of the point.
func (r *Recorder) read(at time.Time) Point {
	p := Point{At: at, Samples: 1}
	if t, err := r.src.CPUTimes(); err == nil {
		if r.prevCPU != nil && t.Total > r.prevCPU.Total {
			busy := 100 * (1 - float64(t.Idle-r.prevCPU.Idle)/float64(t.Total-r.prevCPU.Total))
			p.CPU = round1(min(100, max(0, busy)))
		}
		r.prevCPU = &t
	}
	if m, err := r.src.Memory(); err == nil && m.TotalBytes > 0 {
		used := m.TotalBytes - min(m.AvailableBytes, m.TotalBytes)
		p.Mem = round1(100 * float64(used) / float64(m.TotalBytes))
		p.MemUsed, p.MemTotal = used, m.TotalBytes
		if m.SwapTotalBytes > 0 {
			p.Swap = round1(100 * float64(m.SwapTotalBytes-min(m.SwapFreeBytes, m.SwapTotalBytes)) / float64(m.SwapTotalBytes))
		}
	}
	if l, err := r.src.LoadAverage(); err == nil {
		p.Load1, p.Load5, p.Load15 = round1(l.Load1), round1(l.Load5), round1(l.Load15)
	}
	for _, path := range r.disks() {
		d, err := r.src.DiskUsage(path)
		if err != nil || d.TotalBytes == 0 || d.UsedBytes+d.FreeBytes == 0 {
			continue
		}
		if p.Disks == nil {
			p.Disks = map[string]Disk{}
		}
		p.Disks[path] = Disk{UsedPct: *round1(100 * float64(d.UsedBytes) / float64(d.UsedBytes+d.FreeBytes)),
			UsedBytes: d.UsedBytes, FreeBytes: d.FreeBytes, TotalBytes: d.TotalBytes}
	}
	return p
}
