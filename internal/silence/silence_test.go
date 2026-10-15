// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package silence

import (
	"os"
	"testing"
	"time"
)

func TestAddReplaceClear(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	if _, err := Add(dir, "web", time.Hour, "deploy", now); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, "web", 2*time.Hour, "longer", now); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, "db", time.Minute, "", now); err != nil {
		t.Fatal(err)
	}
	list, err := Load(dir, now)
	if err != nil || len(list) != 2 {
		t.Fatalf("Load = %+v, %v", list, err)
	}
	if s, ok := Covering(list, "web", now); !ok || s.Reason != "longer" {
		t.Fatalf("a new silence should replace the old one: %+v", s)
	}
	if list, _ := Load(dir, now.Add(5*time.Minute)); len(list) != 1 {
		t.Fatalf("ended silences must drop out: %+v", list)
	}
	if n, err := Clear(dir, "db", now); n != 1 || err != nil {
		t.Fatalf("Clear = %d, %v", n, err)
	}
	if n, _ := Clear(dir, All, now); n != 1 {
		t.Fatalf("clearing all should end the rest, got %d", n)
	}
}

func TestLimits(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second, MaxDuration + time.Second} {
		if _, err := Add(t.TempDir(), "x", d, "", time.Now()); err == nil {
			t.Errorf("duration %v should be refused", d)
		}
	}
}

func TestCoveringAll(t *testing.T) {
	now := time.Now()
	list := []Silence{{Monitor: All, Until: now.Add(time.Hour)}, {Monitor: "a", Until: now.Add(time.Minute)}}
	if s, ok := Covering(list, "b", now); !ok || s.Monitor != All {
		t.Fatal("all covers every monitor")
	}
	if s, _ := Covering(list, "a", now); s.Monitor != All {
		t.Fatal("the silence that ends last wins")
	}
	if _, ok := Covering(list, "a", now.Add(2*time.Hour)); ok {
		t.Fatal("nothing covers after the end")
	}
}

func TestWatcherKeepsLastGoodList(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	w := NewWatcher(dir)
	if list, err := w.Current(now); err != nil || len(list) != 0 {
		t.Fatalf("no file: %+v, %v", list, err)
	}
	Add(dir, "web", time.Hour, "", now)
	if list, _ := w.Current(now); len(list) != 1 {
		t.Fatalf("change not picked up: %+v", list)
	}
	os.WriteFile(Path(dir), []byte("{broken"), 0o640)
	list, err := w.Current(now)
	if err == nil || len(list) != 1 {
		t.Fatalf("damaged file: want the last good list and an error, got %+v, %v", list, err)
	}
}
