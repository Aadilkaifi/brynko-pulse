// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Removing pulse on purpose is not a dead server: uninstall tells the relay first,
// and a relay it cannot reach never stops the uninstall.
func TestUninstallSaysGoodbyeToTheRelay(t *testing.T) {
	var mu sync.Mutex
	var got map[string]any
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		json.NewDecoder(r.Body).Decode(&got)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer relay.Close()
	old := watchdogClient
	watchdogClient = relay.Client
	t.Cleanup(func() { watchdogClient = old })

	useFake(t, nil)
	cfg, data := filepath.Join(t.TempDir(), "cfg"), t.TempDir()
	if code, _, errOut := runCmd("init", "--config", cfg); code != exitOK {
		t.Fatal(errOut)
	}
	pulse, err := os.ReadFile(filepath.Join(cfg, "pulse.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(cfg, "pulse.yaml"), append(pulse, []byte("watchdog:\n  key: ${PULSE_BRYNKO_KEY}\n  url: "+relay.URL+"\n")...), 0o640)
	os.WriteFile(filepath.Join(cfg, "secrets.env"), []byte("PULSE_BRYNKO_KEY=k3y\n"), 0o600)

	code, out, errOut := runCmd("uninstall", "--config", cfg, "--data", data)
	if code != exitOK || !strings.Contains(out, "Told the brynko relay to stop watching this server") {
		t.Fatalf("uninstall: %d %q %q", code, out, errOut)
	}
	mu.Lock()
	if got["goodbye"] != true {
		t.Fatalf("check-in = %v, want goodbye", got)
	}
	mu.Unlock()

	relay.Close() // unreachable now: the uninstall still finishes and says so
	code, out, _ = runCmd("uninstall", "--config", cfg, "--data", data)
	if code != exitOK || !strings.Contains(out, "Could not tell the brynko relay") {
		t.Fatalf("relay down: %d %q", code, out)
	}
}
