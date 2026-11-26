// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package changes keeps a timeline of what changed on the machine: commands run as root, sign-ins,
// packages, services, restarts, containers and configuration files. It answers "what changed
// before this broke?".
package changes

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/fsutil"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

const (
	// Keep is how long a change is kept.
	Keep = 90 * 24 * time.Hour
	// maxBytes caps the timeline file; past it the oldest changes go first.
	maxBytes = 20 << 20

	timelineFile = "changes.jsonl"
	stateFile    = "state.json"
	copiesDir    = "files"
	dirPerm      = 0o750
	filePerm     = 0o640
	maxLineBytes = 64 << 10
)

// Read returns the changes from `since` on, newest first. No file yet means no changes.
func Read(dir string, since time.Time) ([]host.Change, error) {
	out, err := readAll(dir, since)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, err
}

func readAll(dir string, since time.Time) ([]host.Change, error) {
	f, err := os.Open(filepath.Join(dir, timelineFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []host.Change
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), maxLineBytes)
	for sc.Scan() {
		var c host.Change
		if json.Unmarshal(sc.Bytes(), &c) != nil {
			continue // a line cut short by a power cut is skipped, not fatal
		}
		if !c.At.Before(since) {
			out = append(out, c)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", timelineFile, err)
	}
	return out, nil
}

func appendChange(dir string, c host.Change) error {
	line, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, timelineFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// prune rewrites the timeline without changes older than Keep, and drops the oldest of the rest
// until the file is at most three quarters of maxBytes, so it is not rewritten on every change.
func prune(dir string, now time.Time) error {
	path := filepath.Join(dir, timelineFile)
	before, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	all, err := readAll(dir, now.Add(-Keep))
	if err != nil {
		return err
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.Before(all[j].At) })
	lines := make([][]byte, 0, len(all))
	size := 0
	for _, c := range all {
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		lines = append(lines, append(b, '\n'))
		size += len(b) + 1
	}
	if before.Size() > maxBytes {
		for len(lines) > 0 && size > maxBytes*3/4 {
			size -= len(lines[0])
			lines = lines[1:]
		}
	}
	buf := make([]byte, 0, size)
	for _, l := range lines {
		buf = append(buf, l...)
	}
	return fsutil.WriteAtomic(path, buf, filePerm)
}

// state is what the recorder remembers between runs.
type state struct {
	Cursors map[string]string `json:"cursors"` // each source's position
	Boot    time.Time         `json:"boot"`    // when the machine last started
	Files   map[string]string `json:"files"`   // each watched file's SHA-256
}

func loadState(dir string) state {
	st := state{Cursors: map[string]string{}, Files: map[string]string{}}
	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Cursors == nil {
		st.Cursors = map[string]string{}
	}
	if st.Files == nil {
		st.Files = map[string]string{}
	}
	return st
}

func saveState(dir string, st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(filepath.Join(dir, stateFile), b, filePerm)
}
