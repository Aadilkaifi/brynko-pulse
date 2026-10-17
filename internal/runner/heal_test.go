// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/Aadilkaifi/brynko-pulse/internal/heal"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/store"
)

// TestHelperTouch is not a test: a fix starts this binary with "-- touch path" and
// it creates the file, which is what fixes the monitor below. With "-- exists path"
// it is the check: exit 1 while the file is missing.
func TestHelperTouch(t *testing.T) {
	for i, a := range os.Args {
		if a != "--" || i+2 >= len(os.Args) {
			continue
		}
		switch path := os.Args[i+2]; os.Args[i+1] {
		case "touch":
			if err := os.WriteFile(path, []byte("restored"), 0o600); err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		case "exists":
			if _, err := os.Stat(path); err != nil {
				fmt.Println("missing")
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
}

// healSetup starts pulse with a command monitor that fails while a file is missing,
// and a heal rule whose step puts the file back.
func healSetup(t *testing.T, mode string) (dataDir, watched string, alerts func() []alert.Alert, stop func()) {
	t.Helper()
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
	t.Cleanup(hook.Close)

	cfgDir := t.TempDir()
	dataDir = t.TempDir()
	watched = filepath.Join(t.TempDir(), "backup.tar")
	check, _ := json.Marshal([]string{os.Args[0], "-test.run=^TestHelperTouch$", "--", "exists", watched})
	fix, _ := json.Marshal([]string{os.Args[0], "-test.run=^TestHelperTouch$", "--", "touch", watched})
	writeFiles(t, cfgDir, map[string]string{
		"pulse.yaml":    "version: 1\nserver_name: test-box\ndefaults:\n  notify: [hook]\n  interval: 1s\n",
		"channels.yaml": "channels:\n  hook:\n    type: webhook\n    url: " + hook.URL + "\n",
		"monitors/m.yaml": "monitors:\n  - id: backup\n    type: command\n    run: " + string(check) +
			"\n    heal:\n      mode: " + mode + "\n      steps:\n        - run: " + string(fix) + "\n      verify: 1m\n",
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{ConfigDir: cfgDir, DataDir: dataDir, Platform: platform.Current(),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)), SkipSecurityCheck: true, Version: "test"})
	}()
	alerts = func() []alert.Alert {
		mu.Lock()
		defer mu.Unlock()
		return append([]alert.Alert(nil), got...)
	}
	stop = func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	return dataDir, watched, alerts, stop
}

func historyKinds(t *testing.T, dataDir string) map[string]int {
	t.Helper()
	recs, _ := store.History(dataDir, 20)
	kinds := map[string]int{}
	for _, r := range recs {
		kinds[r.Kind]++
	}
	return kinds
}

// Auto: the file goes missing, pulse runs the step that puts it back, and people get
// exactly one message: it broke and pulse fixed it.
func TestRunHealsByItselfAndSendsOneMessage(t *testing.T) {
	dataDir, watched, alerts, stop := healSetup(t, "auto")
	waitFor(t, func() bool { return len(alerts()) >= 1 })
	stop()

	got := alerts()
	if len(got) != 1 || got[0].Kind != alert.KindHealed || !strings.Contains(got[0].Heal, "It failed (") ||
		!strings.Contains(got[0].Heal, "pulse ran runner.test") {
		t.Fatalf("alerts = %+v, want one healed message saying what broke and what pulse ran", got)
	}
	if _, err := os.Stat(watched); err != nil {
		t.Fatalf("the fix did not run: %v", err)
	}
	// The monitor may see the file back before the fix has written its own record, so
	// only the records are checked, not their order.
	if k := historyKinds(t, dataDir); k["heal"] != 1 || k[alert.KindHealed] != 1 {
		t.Fatalf("history kinds = %v, want one fix and one healed message", k)
	}
	snap, _ := store.ReadState(dataDir)
	hs := snap.Monitors["backup"].Heal
	if hs == nil || hs.Mode != "auto" || hs.AttemptsUsed != 1 || hs.Attempts != 3 || hs.LastAt.IsZero() {
		t.Fatalf("heal status = %+v", hs)
	}
}

// Ask: the alert says a fix is waiting; nothing runs until it is approved through the
// same request files pulse fix uses; then the fix runs and people hear it worked.
func TestRunWaitsForApprovalThenFixes(t *testing.T) {
	dataDir, watched, alerts, stop := healSetup(t, "ask")
	defer stop()
	waitFor(t, func() bool { return len(alerts()) >= 1 })
	first := alerts()[0]
	if first.Kind != alert.KindFiring || !strings.Contains(first.Heal, "A fix is waiting for your approval: run runner.test") {
		t.Fatalf("first alert = %+v", first)
	}
	if _, err := os.Stat(watched); err == nil {
		t.Fatal("the fix ran without approval")
	}
	var fixID string
	waitFor(t, func() bool {
		snap, _ := store.ReadState(dataDir)
		if snap == nil || snap.Monitors["backup"].Heal == nil || snap.Monitors["backup"].Heal.Waiting == nil {
			return false
		}
		fixID = snap.Monitors["backup"].Heal.Waiting.ID
		return true
	})
	ans, err := heal.Submit(context.Background(), dataDir,
		heal.Request{Monitor: "backup", FixID: fixID, Action: heal.ActionApprove, At: time.Now()}, 10*time.Second)
	if err != nil || !ans.OK || ans.FixID != fixID {
		t.Fatalf("approve: %+v %v", ans, err)
	}
	waitFor(t, func() bool { return len(alerts()) >= 2 })
	last := alerts()[1]
	if last.Kind != alert.KindHealed || !strings.Contains(last.Heal, "after your approval") {
		t.Fatalf("after approval = %+v", last)
	}
	if k := historyKinds(t, dataDir); k["heal"] < 2 {
		t.Fatalf("history kinds = %v, want the approval and the fix recorded", k)
	}
}
