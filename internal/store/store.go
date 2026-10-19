// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package store keeps the current state (state.json) and the alert history
// (history.jsonl) in the data folder.
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/engine"
	"github.com/Aadilkaifi/brynko-pulse/internal/fsutil"
)

const (
	stateFile    = "state.json"
	historyFile  = "history.jsonl"
	historyOld   = "history.1.jsonl"
	maxHistory   = 5 << 20
	dirPerm      = 0o750
	filePerm     = 0o640
	maxLineBytes = 64 << 10
)

// MonitorStatus is what status shows for one monitor.
type MonitorStatus struct {
	engine.State
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	File     string         `json:"file,omitempty"`
	Severity string         `json:"severity"`
	Interval string         `json:"interval"`
	Enabled  bool           `json:"enabled"`         // false: turned off with enabled: false, not checked
	Error    string         `json:"error,omitempty"` // set when the monitor could not start
	Values   map[string]any `json:"values,omitempty"`

	SilencedUntil time.Time `json:"silenced_until,omitzero"`
	SilenceReason string    `json:"silence_reason,omitempty"`

	Heal *HealStatus `json:"heal,omitempty"` // set when the monitor has a heal rule
}

// HealStatus is what self-healing has done for one monitor, and what waits for approval.
type HealStatus struct {
	Mode         string       `json:"mode"`          // ask or auto
	What         string       `json:"what"`          // the fix in words
	Attempts     int          `json:"attempts"`      // automatic fixes allowed within the window
	AttemptsUsed int          `json:"attempts_used"` // automatic fixes within the window
	Healing      bool         `json:"healing,omitempty"`
	GaveUp       bool         `json:"gave_up,omitempty"`
	Waiting      *HealWaiting `json:"waiting,omitempty"`  // a fix prepared and waiting for approval
	Approved     bool         `json:"approved,omitempty"` // it was approved and starts at the next check
	Dismissed    bool         `json:"dismissed,omitempty"`
	LastAt       time.Time    `json:"last_at,omitzero"`
	LastResult   string       `json:"last_result,omitempty"`
}

// HealWaiting is a fix waiting for approval. ID names it, so an approval runs
// only the fix that was prepared for this moment.
type HealWaiting struct {
	ID      string    `json:"id"`
	Since   time.Time `json:"since"`
	Expires time.Time `json:"expires"`
	Why     string    `json:"why,omitempty"` // why it waits instead of running by itself
}

// ChannelStatus is the delivery health of one channel.
type ChannelStatus struct {
	Type        string    `json:"type"`
	LastSent    time.Time `json:"last_sent,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
	Unavailable string    `json:"unavailable,omitempty"`
}

// Snapshot is the content of state.json.
type Snapshot struct {
	Server   string                   `json:"server"`
	PID      int                      `json:"pid"`
	Version  string                   `json:"version"`
	Started  time.Time                `json:"started"`
	Updated  time.Time                `json:"updated"`
	Monitors map[string]MonitorStatus `json:"monitors"`
	Channels map[string]ChannelStatus `json:"channels"`
	Watchdog *WatchdogStatus          `json:"watchdog,omitempty"` // set when the watchdog is on
}

// WatchdogStatus is how the last check-ins with the brynko relay went.
type WatchdogStatus struct {
	LastOK      time.Time `json:"last_ok,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
	Email       []string  `json:"email,omitempty"`    // who the relay alerts
	WhatsApp    []string  `json:"whatsapp,omitempty"` // by WhatsApp, through brynko relay channels
	Skipped     []string  `json:"skipped,omitempty"`  // channels it cannot use (not email)
}

// KindUndelivered is the history kind of an alert a channel could not deliver.
const KindUndelivered = "undelivered"

// Record is one line of alert history.
type Record struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Monitor  string    `json:"monitor"`
	Name     string    `json:"name"`
	Severity string    `json:"severity"`
	Detail   string    `json:"detail"`
	Channels []string  `json:"channels"`
	Silenced bool      `json:"silenced,omitempty"` // held back by a silence, sent nowhere

	// For KindUndelivered: the kind and time of the alert that was not delivered, so a
	// reader can match it to that alert's own record. Detail is the channel's error.
	AlertKind string    `json:"alert_kind,omitempty"`
	AlertAt   time.Time `json:"alert_at,omitzero"`
}

// Store writes to one data folder.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open creates the data folder if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("create data folder %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Save replaces state.json.
func (s *Store) Save(snap Snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return fsutil.WriteAtomic(filepath.Join(s.dir, stateFile), data, filePerm)
}

// Append adds one history record, rotating the file when it grows past 5 MB.
func (s *Store) Append(r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, historyFile)
	if fi, err := os.Stat(path); err == nil && fi.Size()+int64(len(line)) > maxHistory {
		if err := os.Rename(path, filepath.Join(s.dir, historyOld)); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// ReadState loads state.json from dir. It returns nil, nil when there is none yet.
func ReadState(dir string) (*Snapshot, error) {
	data, err := os.ReadFile(filepath.Join(dir, stateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("read %s: %w", stateFile, err)
	}
	return &s, nil
}

// History returns up to limit records, newest first.
func History(dir string, limit int) ([]Record, error) {
	var all []Record
	for _, name := range []string{historyOld, historyFile} {
		f, err := os.Open(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 4096), maxLineBytes)
		for sc.Scan() {
			var r Record
			if json.Unmarshal(sc.Bytes(), &r) == nil {
				all = append(all, r)
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]Record, 0, min(limit, len(all)))
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
	}
	return out, nil
}
