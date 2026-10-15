// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/engine"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if snap, err := ReadState(dir); snap != nil || err != nil {
		t.Fatalf("empty dir: %v, %v", snap, err)
	}
	want := Snapshot{Server: "web-1", Monitors: map[string]MonitorStatus{
		"disk": {State: engine.State{Status: engine.Firing}, Name: "disk"}}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(dir)
	if err != nil || got.Monitors["disk"].Status != engine.Firing {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestHistoryNewestFirstAndRotation(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	for i := 0; i < 5; i++ {
		s.Append(Record{At: time.Unix(int64(i), 0), Monitor: string(rune('a' + i))})
	}
	recs, err := History(dir, 3)
	if err != nil || len(recs) != 3 || recs[0].Monitor != "e" || recs[2].Monitor != "c" {
		t.Fatalf("history = %+v, %v", recs, err)
	}

	big := strings.Repeat("x", 1<<20)
	for i := 0; i < 6; i++ {
		if err := s.Append(Record{Monitor: "big", Detail: big}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, historyOld)); err != nil {
		t.Fatal("history should have rotated")
	}
	fi, _ := os.Stat(filepath.Join(dir, historyFile))
	if fi.Size() > maxHistory {
		t.Fatalf("history is %d bytes, over the cap", fi.Size())
	}
}
