// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package heal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSubmitAndServeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := PrepareRequests(dir); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	var mu sync.Mutex
	var got Request
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				Serve(dir, time.Now(), func(r Request) Answer {
					mu.Lock()
					got = r
					mu.Unlock()
					return Answer{OK: true, FixID: "fix1", What: "restart nginx"}
				})
			}
		}
	}()
	ans, err := Submit(context.Background(), dir, Request{Monitor: "web", Action: ActionApprove, At: time.Now()}, 5*time.Second)
	mu.Lock()
	defer mu.Unlock()
	if err != nil || !ans.OK || ans.FixID != "fix1" || got.Monitor != "web" || got.Action != ActionApprove {
		t.Fatalf("answer %+v, err %v, request %+v", ans, err, got)
	}
	if left, _ := os.ReadDir(RequestsDir(dir)); len(left) != 0 {
		t.Fatalf("files left behind: %v", left)
	}
}

func TestSubmitWithoutAServiceSaysSo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Submit(context.Background(), dir, Request{Monitor: "web", Action: ActionApprove, At: time.Now()}, time.Second); !errors.Is(err, ErrNoService) {
		t.Fatalf("no requests folder: %v", err)
	}
	PrepareRequests(dir)
	if _, err := Submit(context.Background(), dir, Request{Monitor: "web", Action: ActionApprove, At: time.Now()}, 300*time.Millisecond); !errors.Is(err, ErrNoService) {
		t.Fatalf("nobody answering: %v", err)
	}
	if left, _ := os.ReadDir(RequestsDir(dir)); len(left) != 0 {
		t.Fatalf("an unanswered request was left behind: %v", left)
	}
}

func TestServeRefusesLateAndUnknownRequests(t *testing.T) {
	dir := t.TempDir()
	PrepareRequests(dir)
	now := time.Now()
	write := func(name string, r Request) {
		b, _ := json.Marshal(r)
		os.WriteFile(filepath.Join(RequestsDir(dir), name+reqSuffix), b, 0o600)
	}
	write("late", Request{Monitor: "web", Action: ActionApprove, At: now.Add(-2 * requestTTL)})
	write("odd", Request{Monitor: "web", Action: "restart-everything", At: now})
	called := false
	if err := Serve(dir, now, func(Request) Answer { called = true; return Answer{OK: true} }); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("a late or unknown request reached the handler")
	}
	for _, name := range []string{"late", "odd"} {
		var a Answer
		b, err := os.ReadFile(filepath.Join(RequestsDir(dir), name+ansSuffix))
		if err != nil || json.Unmarshal(b, &a) != nil || a.OK || a.Error == "" {
			t.Fatalf("%s: answer %s, %v", name, b, err)
		}
	}
}

func TestServeCreatesTheFolder(t *testing.T) {
	dir := t.TempDir()
	if err := Serve(dir, time.Now(), func(Request) Answer { return Answer{} }); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(RequestsDir(dir)); err != nil || !fi.IsDir() {
		t.Fatalf("folder: %v", err)
	}
}
