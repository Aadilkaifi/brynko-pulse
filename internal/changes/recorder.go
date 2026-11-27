// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package changes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

const (
	// dedupeWindow: the same change (kind, who, what) again within this time is not recorded again.
	// Tools that sign in or run the same read every few seconds would otherwise fill the timeline.
	dedupeWindow = time.Hour
	// backfill is how far back sources read the first time.
	backfill = 24 * time.Hour
	// attributeWithin is how long before a file change a sudo command that names the file is taken
	// as the likely cause.
	attributeWithin = 15 * time.Minute
)

// Options are what the recorder reads from.
type Options struct {
	Sources []host.ChangeSource           // the system's own records (journal, apt, event log)
	Uptime  func() (time.Duration, error) // to notice a restart
	Docker  *http.Client                  // the Docker engine; nil when it is not read
	Files   func() []string               // configuration files to fingerprint, asked each scan
	Every   time.Duration                 // how often files and Docker are read; 30s when zero
	Log     *slog.Logger
}

// Recorder writes the timeline. Sources run in their own goroutines and report through Emit.
type Recorder struct {
	dir string
	o   Options

	mu      sync.Mutex
	st      state
	dirty   bool
	seen    map[string]time.Time // last time each change was recorded, for dedupeWindow
	sudo    []host.Change        // recent sudo commands, to say who likely changed a file
	compose map[string]bool      // compose files learned from container labels
	stats   map[string]fileStat  // size and time of each watched file, so unchanged files are not reread
	pruned  time.Time

	errMu   sync.Mutex
	errored map[string]bool // sources whose failure was already logged as a warning
}

// NewRecorder records into dir, creating it when needed.
func NewRecorder(dir string, o Options) (*Recorder, error) {
	if err := os.MkdirAll(filepath.Join(dir, copiesDir), dirPerm); err != nil {
		return nil, fmt.Errorf("create changes folder %s: %w", dir, err)
	}
	if o.Every <= 0 {
		o.Every = 30 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	r := &Recorder{dir: dir, o: o, st: loadState(dir), seen: map[string]time.Time{}, compose: map[string]bool{},
		stats: map[string]fileStat{}, errored: map[string]bool{}}
	// The last hour is remembered across a restart, so a restart does not record it all again.
	recent, _ := readAll(dir, time.Now().Add(-dedupeWindow))
	for _, c := range recent {
		r.seen[dedupeKey(c)] = c.At
		if c.Kind == host.ChangeSudo {
			r.sudo = append(r.sudo, c)
		}
	}
	return r, nil
}

// Run records until ctx ends.
func (r *Recorder) Run(ctx context.Context) {
	now := time.Now()
	r.CheckBoot(now)
	var wg sync.WaitGroup
	for _, s := range r.o.Sources {
		wg.Add(1)
		go func() { defer wg.Done(); r.follow(ctx, s) }()
	}
	tick := time.NewTicker(r.o.Every)
	defer tick.Stop()
	nextContainers := time.Time{}
	for {
		at := time.Now()
		if r.o.Docker != nil {
			if !at.Before(nextContainers) {
				r.learnComposeFiles(ctx)
				nextContainers = at.Add(10 * time.Minute)
			}
			r.pollDocker(ctx, at)
		}
		r.ScanFiles(at)
		r.housekeep(at)
		select {
		case <-ctx.Done():
			wg.Wait()
			r.save()
			return
		case <-tick.C:
		}
	}
}

// follow runs one source, starting it again a minute after it fails.
func (r *Recorder) follow(ctx context.Context, s host.ChangeSource) {
	for ctx.Err() == nil {
		r.mu.Lock()
		cursor := r.st.Cursors[s.Name()]
		r.mu.Unlock()
		err := s.Follow(ctx, cursor, r.Emit, func(c string) { r.checkpoint(s.Name(), c) })
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, host.ErrUnsupported) {
			r.o.Log.Info("changes: source not available on this system", "source", s.Name(), "reason", err.Error())
			return
		}
		r.setProblem(s.Name(), err)
		r.warnOnce(s.Name(), "changes: source stopped; trying again in a minute", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}

// Emit records one change, unless the same change was recorded within the last hour.
func (r *Recorder) Emit(c host.Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emit(c)
}

func (r *Recorder) emit(c host.Change) bool {
	c.At = c.At.UTC().Truncate(time.Second)
	key := dedupeKey(c)
	if last, ok := r.seen[key]; ok && absDuration(c.At.Sub(last)) < dedupeWindow {
		return false
	}
	r.seen[key] = c.At
	if c.Kind == host.ChangeSudo {
		r.sudo = append(r.sudo, c)
		if len(r.sudo) > 500 {
			r.sudo = r.sudo[len(r.sudo)-500:]
		}
	}
	if err := appendChange(r.dir, c); err != nil {
		r.warnOnce("store", "changes: not recorded", err)
		return false
	}
	r.clearProblemLocked(c.Source) // a source that records works
	return true
}

func (r *Recorder) checkpoint(source, cursor string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearProblemLocked(source)
	if r.st.Cursors[source] != cursor {
		r.st.Cursors[source] = cursor
		r.dirty = true
	}
}

// setProblem notes why a source records nothing, so `pulse changes` can say so instead of showing
// a timeline that only looks quiet. It is cleared when the source works again.
func (r *Recorder) setProblem(source string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setProblemLocked(source, err.Error())
}

func (r *Recorder) setProblemLocked(source, problem string) {
	if r.st.Problems[source] != problem {
		r.st.Problems[source] = problem
		r.dirty = true
	}
}

func (r *Recorder) clearProblemLocked(source string) {
	if _, ok := r.st.Problems[source]; ok {
		delete(r.st.Problems, source)
		r.dirty = true
	}
}

// CheckBoot records a restart when the machine started at a different time than pulse last saw.
func (r *Recorder) CheckBoot(now time.Time) {
	if r.o.Uptime == nil {
		return
	}
	up, err := r.o.Uptime()
	if err != nil {
		return
	}
	boot := now.Add(-up).UTC().Truncate(time.Minute)
	r.mu.Lock()
	defer r.mu.Unlock()
	if absDuration(boot.Sub(r.st.Boot)) <= 2*time.Minute {
		return
	}
	if !r.st.Boot.IsZero() || now.Sub(boot) < backfill {
		r.emit(host.Change{At: boot, Kind: host.ChangeReboot, What: "the machine started", Source: "boot"})
	}
	r.st.Boot = boot
	r.dirty = true
}

// housekeep saves positions and, once an hour, drops what is older than Keep.
func (r *Recorder) housekeep(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.pruned) >= time.Hour {
		if err := prune(r.dir, now); err != nil {
			r.warnOnce("prune", "changes: old changes not pruned", err)
		}
		for k, at := range r.seen {
			if now.Sub(at) > 2*dedupeWindow {
				delete(r.seen, k)
			}
		}
		keep := r.sudo[:0]
		for _, c := range r.sudo {
			if now.Sub(c.At) <= attributeWithin {
				keep = append(keep, c)
			}
		}
		r.sudo = keep
		r.pruned = now
	}
	r.saveLocked()
}

func (r *Recorder) save() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveLocked()
}

func (r *Recorder) saveLocked() {
	if !r.dirty {
		return
	}
	if err := saveState(r.dir, r.st); err != nil {
		r.warnOnce("state", "changes: positions not saved", err)
		return
	}
	r.dirty = false
}

// warnOnce logs a failure as a warning the first time and quietly after, so a source that cannot
// work here (no Docker access, say) does not fill the log.
func (r *Recorder) warnOnce(key, msg string, err error) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	if r.errored[key] {
		r.o.Log.Debug(msg, "source", key, "error", fmt.Sprint(err))
		return
	}
	r.errored[key] = true
	r.o.Log.Warn(msg, "source", key, "error", fmt.Sprint(err))
}

func dedupeKey(c host.Change) string { return c.Kind + "\x00" + c.Who + "\x00" + c.What }

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
