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

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/heal"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/store"
)

// TestHelperTouch is not a test: a run fix starts this binary with "--" and a
// path, and it creates the file, which is what fixes the monitor below.
func TestHelperTouch(t *testing.T) {
	for i, a := range os.Args {
		if a == "--" && i+1 < len(os.Args) {
			if err := os.WriteFile(os.Args[i+1], []byte("restored"), 0o600); err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
}

// End to end: a file goes missing, the heal rule runs a program that puts it back,
// and people get exactly one message: it broke and pulse fixed it.
func TestRunHealsAndSendsOneMessage(t *testing.T) {
	old := heal.MinDelay
	heal.MinDelay = 0
	t.Cleanup(func() { heal.MinDelay = old })

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
	run, _ := json.Marshal([]string{os.Args[0], "-test.run=^TestHelperTouch$", "--", watched})
	writeFiles(t, cfgDir, map[string]string{
		"pulse.yaml":    "version: 1\nserver_name: test-box\ndefaults:\n  notify: [hook]\n  interval: 1s\n",
		"channels.yaml": "channels:\n  hook:\n    type: webhook\n    url: " + hook.URL + "\n",
		"monitors/m.yaml": "monitors:\n  - id: backup\n    type: file\n    path: " + filepath.ToSlash(watched) +
			"\n    alert_when: age > 1d\n    heal:\n      action: run\n      run: " + string(run) + "\n      verify: 1m\n",
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{ConfigDir: cfgDir, DataDir: dataDir, Platform: platform.Current(),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)), SkipSecurityCheck: true, Version: "test"})
	}()

	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Kind != alert.KindHealed || !strings.Contains(got[0].Heal, "does not exist") ||
		!strings.Contains(got[0].Heal, "pulse ran runner.test") {
		t.Fatalf("alerts = %+v, want one healed message saying what broke and what pulse ran", got)
	}
	if _, err := os.Stat(watched); err != nil {
		t.Fatalf("the fix did not run: %v", err)
	}
	recs, _ := store.History(dataDir, 10)
	kinds := map[string]int{}
	for _, r := range recs {
		kinds[r.Kind]++
	}
	// The monitor may see the file back before the fix has written its own record, so
	// only the records are checked, not their order.
	if len(recs) != 2 || kinds["heal"] != 1 || kinds[alert.KindHealed] != 1 {
		t.Fatalf("history = %+v, want one fix and one healed message", recs)
	}
	snap, _ := store.ReadState(dataDir)
	hs := snap.Monitors["backup"].Heal
	if hs == nil || hs.Action != "run" || hs.AttemptsUsed != 1 || hs.Attempts != 3 || hs.LastAt.IsZero() {
		t.Fatalf("heal status = %+v", hs)
	}
}
