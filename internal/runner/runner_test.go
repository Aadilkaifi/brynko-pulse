// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/engine"
	"github.com/Aadilkaifi/brynko-pulse/internal/notify"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/silence"
	"github.com/Aadilkaifi/brynko-pulse/internal/store"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o750)
		if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

// End to end: a file that does not exist fires an alert to a webhook,
// creating it resolves the alert, and both land in state and history.
func TestRunFiresAndResolves(t *testing.T) {
	var mu sync.Mutex
	var got []alert.Alert
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a alert.Alert
		json.NewDecoder(r.Body).Decode(&a)
		mu.Lock()
		got = append(got, a)
		mu.Unlock()
	}))
	defer hook.Close()

	cfgDir, dataDir := t.TempDir(), t.TempDir()
	watched := filepath.Join(t.TempDir(), "backup.tar")
	writeFiles(t, cfgDir, map[string]string{
		"pulse.yaml":    "version: 1\nserver_name: test-box\ndefaults:\n  notify: [hook]\n  interval: 1s\n",
		"channels.yaml": "channels:\n  hook:\n    type: webhook\n    url: " + hook.URL + "\n",
		"monitors/m.yaml": "monitors:\n  - id: backup\n    type: file\n    path: " + filepath.ToSlash(watched) +
			"\n    alert_when: age > 1d\n    severity: critical\n",
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{ConfigDir: cfgDir, DataDir: dataDir, Platform: platform.Current(),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)), SkipSecurityCheck: true, Version: "test"})
	}()

	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= 1 })
	os.WriteFile(watched, []byte("x"), 0o600)
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	if got[0].Kind != alert.KindFiring || !strings.Contains(got[0].Detail, "does not exist") || got[1].Kind != alert.KindResolved {
		t.Fatalf("alerts = %+v", got)
	}
	mu.Unlock()

	snap, err := store.ReadState(dataDir)
	if err != nil || snap == nil || snap.Monitors["backup"].Status != engine.OK || snap.Server != "test-box" {
		t.Fatalf("state = %+v, %v", snap, err)
	}
	recs, _ := store.History(dataDir, 10)
	if len(recs) != 2 || recs[0].Kind != alert.KindResolved || len(recs[1].Channels) != 1 {
		t.Fatalf("history = %+v", recs)
	}
}

// Right after an install, `pulse status` must find a state, not report pulse as never run until the
// first save tick five seconds later.
func TestRunReportsAsSoonAsItStarts(t *testing.T) {
	cfgDir, dataDir := t.TempDir(), t.TempDir()
	writeFiles(t, cfgDir, map[string]string{
		"pulse.yaml":      "version: 1\nserver_name: test-box\ndefaults:\n  interval: 1h\n",
		"monitors/m.yaml": "monitors:\n  - id: mem\n    type: memory\n    alert_when: used_percent > 99\n",
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{ConfigDir: cfgDir, DataDir: dataDir, Platform: platform.Current(),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)), SkipSecurityCheck: true, Version: "test"})
	}()
	defer func() { cancel(); <-done }()

	deadline := time.Now().Add(2 * time.Second) // well before the first save tick (saveEvery)
	for time.Now().Before(deadline) {
		if snap, err := store.ReadState(dataDir); err == nil && snap != nil {
			if _, ok := snap.Monitors["mem"]; !ok || snap.Server != "test-box" {
				t.Fatalf("first state = %+v", snap)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no state %v after start; the first save came only with the %v tick", 2*time.Second, saveEvery)
}

func TestRunRefusesInvalidConfig(t *testing.T) {
	cfgDir := t.TempDir()
	writeFiles(t, cfgDir, map[string]string{"pulse.yaml": "version: 2\nserver_name: x\n"})
	err := Run(context.Background(), Options{ConfigDir: cfgDir, DataDir: t.TempDir(), Platform: platform.Current(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), SkipSecurityCheck: true})
	if err == nil || !strings.Contains(err.Error(), "pulse validate") {
		t.Fatalf("err = %v", err)
	}
}

func TestReloadKeepsRunningOnBadConfig(t *testing.T) {
	cfgDir, dataDir := t.TempDir(), t.TempDir()
	writeFiles(t, cfgDir, map[string]string{
		"pulse.yaml":      "version: 1\nserver_name: before\n",
		"monitors/m.yaml": "monitors:\n  - id: mem\n    type: memory\n    alert_when: used_percent > 101\n",
	})
	reload := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{ConfigDir: cfgDir, DataDir: dataDir, Platform: platform.Current(),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)), SkipSecurityCheck: true, Reload: reload})
	}()

	waitFor(t, func() bool {
		s, _ := store.ReadState(dataDir)
		return s != nil && s.Server == "before"
	})
	writeFiles(t, cfgDir, map[string]string{"pulse.yaml": "version: 1\nserver_name: [broken\n"})
	reload <- struct{}{}
	writeFiles(t, cfgDir, map[string]string{"pulse.yaml": "version: 1\nserver_name: after\n"})
	reload <- struct{}{}
	waitFor(t, func() bool {
		s, _ := store.ReadState(dataDir)
		return s != nil && s.Server == "after"
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A silence holds alerts back; when it ends with the problem still there,
// exactly one alert goes out.
func TestSilenceHoldsThenReminds(t *testing.T) {
	var mu sync.Mutex
	var got []alert.Alert
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a alert.Alert
		json.NewDecoder(r.Body).Decode(&a)
		mu.Lock()
		got = append(got, a)
		mu.Unlock()
	}))
	defer hook.Close()

	cfgDir, dataDir := t.TempDir(), t.TempDir()
	writeFiles(t, cfgDir, map[string]string{
		"pulse.yaml":    "version: 1\nserver_name: box\ndefaults:\n  notify: [hook]\n  interval: 1s\n",
		"channels.yaml": "channels:\n  hook:\n    type: webhook\n    url: " + hook.URL + "\n",
		"monitors/m.yaml": "monitors:\n  - id: gone\n    type: file\n    path: " +
			filepath.ToSlash(filepath.Join(t.TempDir(), "missing")) + "\n    alert_when: age > 1d\n",
	})
	if _, err := silence.Add(dataDir, silence.All, 3*time.Second, "deploy", time.Now()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{ConfigDir: cfgDir, DataDir: dataDir, Platform: platform.Current(),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)), SkipSecurityCheck: true})
	}()

	waitFor(t, func() bool {
		recs, _ := store.History(dataDir, 10)
		return len(recs) == 1 && recs[0].Silenced
	})
	mu.Lock()
	if len(got) != 0 {
		t.Fatalf("sent during the silence: %+v", got)
	}
	mu.Unlock()
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= 1 })
	time.Sleep(2500 * time.Millisecond) // more checks; the cooldown must keep them quiet
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Kind != alert.KindFiring || !strings.Contains(got[0].Detail, "after the silence ended") {
		t.Fatalf("want one reminder after the silence, got %+v", got)
	}
}

// An alert a channel could not deliver gets its own history line, tied to the alert by its
// kind and time, so "sent to email" is never read as "email arrived".
func TestUndeliveredAlertIsInHistory(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := &runner{o: Options{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, st: st}
	at := time.Date(2026, 10, 17, 20, 23, 20, 0, time.UTC)
	r.undelivered(notify.Failure{Channel: "email", Err: "connect to smtp.example.co:587: no such host",
		Alert: alert.Alert{Monitor: "api", MonitorName: "API", Kind: alert.KindFiring, Severity: "critical", At: at}})
	recs, _ := store.History(dir, 5)
	if len(recs) != 1 {
		t.Fatalf("history = %+v", recs)
	}
	got := recs[0]
	if got.Kind != store.KindUndelivered || got.Monitor != "api" || got.Name != "API" || got.Severity != "critical" ||
		got.AlertKind != alert.KindFiring || !got.AlertAt.Equal(at) || len(got.Channels) != 1 || got.Channels[0] != "email" ||
		!strings.Contains(got.Detail, "no such host") {
		t.Fatalf("record = %+v", got)
	}
}

// A restart keeps a channel's last delivery error while channels.yaml and secrets.env are
// as they were when it was saved, and starts fresh once either changed.
func TestRestartKeepsChannelHealth(t *testing.T) {
	for _, changed := range []bool{false, true} {
		cfgDir, dataDir := t.TempDir(), t.TempDir()
		writeFiles(t, cfgDir, map[string]string{
			"pulse.yaml":      "version: 1\nserver_name: test-box\ndefaults:\n  interval: 1h\n",
			"channels.yaml":   "channels:\n  hook:\n    type: webhook\n    url: https://example.invalid/h\n",
			"monitors/m.yaml": "monitors:\n  - id: mem\n    type: memory\n    alert_when: used_percent > 99\n",
		})
		old := time.Now().Add(-time.Hour)
		for _, f := range []string{"pulse.yaml", "channels.yaml", "monitors/m.yaml"} {
			os.Chtimes(filepath.Join(cfgDir, filepath.FromSlash(f)), old, old)
		}
		st, _ := store.Open(dataDir)
		st.Save(store.Snapshot{Server: "test-box", Updated: time.Now().Add(-time.Minute),
			Channels: map[string]store.ChannelStatus{"hook": {Type: "webhook", LastError: "no such host",
				LastErrorAt: time.Now().Add(-2 * time.Minute)}}})
		if changed {
			os.Chtimes(filepath.Join(cfgDir, "channels.yaml"), time.Now(), time.Now())
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, Options{ConfigDir: cfgDir, DataDir: dataDir, Platform: platform.Current(),
				Log: slog.New(slog.NewTextHandler(io.Discard, nil)), SkipSecurityCheck: true, Version: "test"})
		}()
		waitFor(t, func() bool {
			snap, err := store.ReadState(dataDir)
			return err == nil && snap != nil && snap.PID != 0
		})
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		snap, _ := store.ReadState(dataDir)
		if got := snap.Channels["hook"].LastError; (got == "no such host") == changed {
			t.Fatalf("changed=%v: last error after restart = %q", changed, got)
		}
	}
}
