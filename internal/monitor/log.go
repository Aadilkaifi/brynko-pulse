// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
)

const (
	maxLogLine     = 64 << 10 // longer lines are cut, so one huge line cannot use unbounded memory
	maxLogMatches  = 10_000   // timestamps kept for the rate; beyond this the count saturates
	logPollEvery   = time.Second
	maxReadPerPoll = 4 << 20
	lastLineChars  = 300
)

// logMonitor follows a file, the systemd journal or a Windows Event Log
// channel and counts the lines that match within the window.
type logMonitor struct {
	source  config.LogSource
	re      *regexp.Regexp
	window  time.Duration
	p       platform.Platform
	log     *slog.Logger
	started time.Time

	mu      sync.Mutex
	matches []time.Time
	last    string
	err     error
	running bool
}

func newLog(m config.Monitor, d Deps) (Checker, error) {
	if m.Source == nil {
		return nil, errors.New("a log monitor needs a source")
	}
	re, err := regexp.Compile(m.Pattern)
	if err != nil {
		return nil, fmt.Errorf("pattern is not a valid regular expression: %w", err)
	}
	log := d.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &logMonitor{source: *m.Source, re: re, window: orDur(m.Window, time.Minute), p: d.Platform,
		log: log, started: time.Now()}, nil
}

func (c *logMonitor) Check(context.Context) (rule.Values, error) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	if !c.running {
		return nil, ErrNotReady
	}
	cut := now.Add(-c.window)
	keep := c.matches[:0]
	for _, t := range c.matches {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	c.matches = keep
	v := rule.Values{"matches": float64(len(keep))}
	if c.last != "" {
		v["last_line"] = c.last
	}
	return v, nil
}

// Watch follows the source until ctx ends and asks for a check on each match.
func (c *logMonitor) Watch(ctx context.Context, changed func()) {
	onLine := func(line string) {
		if !c.re.MatchString(line) {
			return
		}
		c.mu.Lock()
		if len(c.matches) < maxLogMatches {
			c.matches = append(c.matches, time.Now())
		}
		c.last = truncate(line, lastLineChars)
		c.mu.Unlock()
		changed()
	}
	c.setRunning(nil)
	var err error
	switch {
	case c.source.File != "":
		err = followFile(ctx, c.source.File, onLine)
	case c.source.Journal != nil:
		err = followJournal(ctx, *c.source.Journal, onLine, c.log)
	case c.source.EventLog != "":
		err = c.p.FollowEventLog(ctx, c.source.EventLog, onLine)
	default:
		err = errors.New("the log source is empty")
	}
	if err != nil && ctx.Err() == nil {
		c.setRunning(err)
		changed()
	}
}

func (c *logMonitor) setRunning(err error) {
	c.mu.Lock()
	c.running, c.err = err == nil, err
	c.mu.Unlock()
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// followFile reads lines appended after it starts, like tail -F: it notices
// when the file is replaced or truncated, as log rotation does, and starts over.
func followFile(ctx context.Context, path string, onLine func(string)) error {
	var f *os.File
	var pos int64
	var info os.FileInfo
	open := func(fromEnd bool) {
		if f != nil {
			f.Close()
			f = nil
		}
		nf, err := os.Open(path)
		if err != nil {
			return
		}
		fi, err := nf.Stat()
		if err != nil {
			nf.Close()
			return
		}
		f, info, pos = nf, fi, 0
		if fromEnd {
			pos = fi.Size()
		}
	}
	open(true)
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	var partial []byte
	buf := make([]byte, 64<<10)
	t := time.NewTicker(logPollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		fi, err := os.Stat(path)
		switch {
		case err != nil:
			continue // gone for now, for example mid-rotation
		case f == nil || !os.SameFile(fi, info):
			open(false) // a new file: read it from the start
			partial = partial[:0]
		case fi.Size() < pos:
			pos, partial = 0, partial[:0] // truncated in place
		}
		if f == nil {
			continue
		}
		read := 0
		for read < maxReadPerPoll {
			n, err := f.ReadAt(buf, pos)
			if n > 0 {
				pos += int64(n)
				read += n
				partial = splitLines(append(partial, buf[:n]...), onLine)
			}
			if err != nil || n == 0 {
				break
			}
		}
	}
}

// splitLines calls onLine for each complete line and returns what is left.
func splitLines(data []byte, onLine func(string)) []byte {
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if len(data) > maxLogLine {
				onLine(string(data[:maxLogLine]))
				return data[:0]
			}
			return data
		}
		line := data[:i]
		if len(line) > maxLogLine {
			line = line[:maxLogLine]
		}
		onLine(strings.TrimRight(string(line), "\r"))
		data = data[i+1:]
	}
}

// followJournal runs journalctl and reads its JSON output. An empty unit
// follows the whole journal.
func followJournal(ctx context.Context, unit string, onLine func(string), log *slog.Logger) error {
	if strings.HasPrefix(unit, "-") {
		return fmt.Errorf("invalid unit name %q", unit)
	}
	if _, err := exec.LookPath("journalctl"); err != nil {
		return errors.New("journalctl was not found, so the journal cannot be followed")
	}
	args := []string{"--follow", "--output=json", "--lines=0", "--quiet"}
	if unit != "" {
		args = append(args, "--unit="+unit)
	}
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := runJournal(ctx, args, onLine)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		log.Warn("journalctl stopped; starting it again", "error", fmt.Sprint(err), "in", backoff.String())
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return nil
}

func runJournal(ctx context.Context, args []string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var entry struct {
			Message json.RawMessage `json:"MESSAGE"`
		}
		if json.Unmarshal(sc.Bytes(), &entry) != nil {
			continue
		}
		onLine(journalText(entry.Message))
	}
	return cmd.Wait()
}

// journalText decodes MESSAGE, which journald gives as a string, or as an
// array of bytes when it is not valid text.
func journalText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var b []byte
	var ints []int
	if json.Unmarshal(raw, &ints) == nil {
		for _, i := range ints {
			b = append(b, byte(i))
		}
		return string(b)
	}
	return ""
}
