// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package silence keeps the list of monitors whose alerts are paused, for
// example during a deploy. Monitors keep being checked; only sending stops.
package silence

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/fsutil"
)

// All silences every monitor on the server.
const All = "all"

// MaxDuration keeps a forgotten silence from hiding problems for good.
const MaxDuration = 30 * 24 * time.Hour

const (
	fileName = "silences.json"
	maxSize  = 1 << 20
)

// Silence pauses alerts for one monitor, or for all of them, until a time.
type Silence struct {
	Monitor string    `json:"monitor"`
	Until   time.Time `json:"until"`
	Reason  string    `json:"reason,omitempty"`
	Created time.Time `json:"created"`
}

type file struct {
	Silences []Silence `json:"silences"`
}

// Path is where silences are kept in the data folder.
func Path(dataDir string) string { return filepath.Join(dataDir, fileName) }

// Load returns the silences that have not ended yet.
func Load(dataDir string, now time.Time) ([]Silence, error) {
	data, err := os.ReadFile(Path(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return []Silence{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read silences: %w", err)
	}
	if len(data) > maxSize {
		return nil, errors.New("silences.json is too large")
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("silences.json is damaged: %w", err)
	}
	return active(f.Silences, now), nil
}

// Add silences monitor for d, replacing any earlier silence on it.
func Add(dataDir, monitor string, d time.Duration, reason string, now time.Time) (Silence, error) {
	if err := CheckDuration(d); err != nil {
		return Silence{}, err
	}
	list, err := Load(dataDir, now)
	if err != nil {
		return Silence{}, err
	}
	s := Silence{Monitor: monitor, Until: now.Add(d).Truncate(time.Second), Reason: reason, Created: now.Truncate(time.Second)}
	out := []Silence{s}
	for _, x := range list {
		if x.Monitor != monitor {
			out = append(out, x)
		}
	}
	return s, save(dataDir, out)
}

// CheckDuration rejects silences that are empty or longer than MaxDuration.
func CheckDuration(d time.Duration) error {
	if d <= 0 || d > MaxDuration {
		return fmt.Errorf("a silence must last between 1s and %d days", int(MaxDuration.Hours()/24))
	}
	return nil
}

// Clear ends the silence on monitor. All ends every silence. It returns how many ended.
func Clear(dataDir, monitor string, now time.Time) (int, error) {
	list, err := Load(dataDir, now)
	if err != nil {
		return 0, err
	}
	keep := []Silence{}
	for _, x := range list {
		if monitor != All && x.Monitor != monitor {
			keep = append(keep, x)
		}
	}
	if removed := len(list) - len(keep); removed > 0 {
		return removed, save(dataDir, keep)
	}
	return 0, nil
}

func save(dataDir string, list []Silence) error {
	sort.Slice(list, func(i, j int) bool { return list[i].Monitor < list[j].Monitor })
	data, err := json.MarshalIndent(file{Silences: list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return err
	}
	return fsutil.WriteAtomic(Path(dataDir), data, 0o640)
}

func active(list []Silence, now time.Time) []Silence {
	out := []Silence{}
	for _, s := range list {
		if s.Until.After(now) {
			out = append(out, s)
		}
	}
	return out
}

// Covering returns the silence that applies to monitor at now, preferring the
// one that ends last.
func Covering(list []Silence, monitor string, now time.Time) (Silence, bool) {
	var best Silence
	found := false
	for _, s := range list {
		if (s.Monitor == monitor || s.Monitor == All) && s.Until.After(now) && (!found || s.Until.After(best.Until)) {
			best, found = s, true
		}
	}
	return best, found
}

// Watcher rereads silences.json only when it changes, so checking before every
// alert costs one stat call.
type Watcher struct {
	dataDir string
	mu      sync.Mutex
	mod     time.Time
	size    int64
	list    []Silence
	loaded  bool
}

// NewWatcher watches the silences in dataDir.
func NewWatcher(dataDir string) *Watcher { return &Watcher{dataDir: dataDir} }

// Current returns the silences in force. When the file cannot be read it keeps
// the last good list and returns the error, so a damaged file never silences more.
func (w *Watcher) Current(now time.Time) ([]Silence, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fi, err := os.Stat(Path(w.dataDir))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		w.list, w.loaded, w.mod, w.size = nil, true, time.Time{}, 0
	case err != nil:
		return active(w.list, now), err
	case !w.loaded || !fi.ModTime().Equal(w.mod) || fi.Size() != w.size:
		list, err := Load(w.dataDir, now)
		if err != nil {
			return active(w.list, now), err
		}
		w.list, w.loaded, w.mod, w.size = list, true, fi.ModTime(), fi.Size()
	}
	return active(w.list, now), nil
}
