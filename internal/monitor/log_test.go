// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
)

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); !cond(); {
		if time.Now().After(end) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLogFileFollowsAppendsAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	os.WriteFile(path, []byte("ERROR old line, before the monitor started\n"), 0o640)
	c, err := newLog(config.Monitor{Source: &config.LogSource{File: path}, Pattern: `ERROR`}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	lm := c.(*logMonitor)
	if _, err := lm.Check(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("before watching: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var kicks atomic.Int32
	go lm.Watch(ctx, func() { kicks.Add(1) })
	time.Sleep(300 * time.Millisecond)

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("INFO fine\nERROR one\nERROR tw")
	f.Close()
	count := func() float64 {
		v, err := lm.Check(context.Background())
		if err != nil {
			return -1
		}
		return v["matches"].(float64)
	}
	waitUntil(t, func() bool { return count() == 1 })
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("o\n")
	f.Close()
	waitUntil(t, func() bool { return count() == 2 })

	// Rotation: the file is moved away and a new one starts.
	os.Rename(path, path+".1")
	os.WriteFile(path, []byte("ERROR after rotation\n"), 0o640)
	waitUntil(t, func() bool { return count() == 3 })
	v, _ := lm.Check(context.Background())
	if v["last_line"] != "ERROR after rotation" || kicks.Load() < 3 {
		t.Fatalf("last_line %v, kicks %d", v["last_line"], kicks.Load())
	}
}

func TestLogWindow(t *testing.T) {
	lm := &logMonitor{window: time.Minute, running: true}
	lm.matches = []time.Time{time.Now().Add(-2 * time.Minute), time.Now().Add(-10 * time.Second)}
	v, err := lm.Check(context.Background())
	if err != nil || v["matches"].(float64) != 1 {
		t.Fatalf("got %v %v", v, err)
	}
}

func TestSplitLines(t *testing.T) {
	var got []string
	rest := splitLines([]byte("a\r\nb\nparti"), func(s string) { got = append(got, s) })
	if strings.Join(got, "|") != "a|b" || string(rest) != "parti" {
		t.Fatalf("got %v rest %q", got, rest)
	}
	got = nil
	splitLines([]byte(strings.Repeat("x", maxLogLine+5)), func(s string) { got = append(got, s) })
	if len(got) != 1 || len(got[0]) != maxLogLine {
		t.Fatal("an endless line must be cut, not kept in memory")
	}
}

func TestJournalText(t *testing.T) {
	if journalText(json.RawMessage(`"hello"`)) != "hello" || journalText(json.RawMessage(`[104,105]`)) != "hi" {
		t.Fatal("journal MESSAGE forms")
	}
}

func TestLogBadPattern(t *testing.T) {
	if _, err := newLog(config.Monitor{Source: &config.LogSource{File: "x"}, Pattern: "("}, Deps{}); err == nil {
		t.Fatal("bad regex must be refused")
	}
}

func TestFileWatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	c := &file{path: path}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var kicks atomic.Int32
	go c.Watch(ctx, func() { kicks.Add(1) })
	time.Sleep(100 * time.Millisecond)
	os.WriteFile(path, []byte("x"), 0o640)
	waitUntil(t, func() bool { return kicks.Load() >= 1 })
}

func TestDockerEventsKickByName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("docker socket")
	}
	sock := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"Type":"container","Action":"die","Actor":{"ID":"abc","Attributes":{"name":"web"}}}` + "\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})}
	go srv.Serve(ln)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := NewDockerEvents(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.client.Transport.(*http.Transport).DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}
	var web, other atomic.Int32
	e.watch("web", func() { web.Add(1) })
	e.watch("db", func() { other.Add(1) })
	waitUntil(t, func() bool { return web.Load() == 1 })
	if other.Load() != 0 {
		t.Fatal("only the named container is checked")
	}
}
