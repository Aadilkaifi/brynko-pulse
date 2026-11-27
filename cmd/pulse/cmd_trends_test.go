// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/trends"
)

// writeTrends stores readings the way the recorder does: every minute for the last hour, and a week of
// 15-minute averages in which /data grows by 2 GB a day.
func writeTrends(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	dir := filepath.Join(data, "trends")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	const gb = 1 << 30
	now := time.Now().UTC().Truncate(time.Minute)
	line := func(f *os.File, p trends.Point) {
		b, _ := json.Marshal(p)
		_, _ = f.Write(append(b, '\n'))
	}
	cpu, mem := 30.0, 60.0
	minute, _ := os.Create(filepath.Join(dir, "minute.jsonl"))
	for i := 60; i >= 0; i-- {
		line(minute, trends.Point{At: now.Add(-time.Duration(i) * time.Minute), Samples: 1, CPU: &cpu, Mem: &mem,
			Disks: map[string]trends.Disk{"/": {UsedPct: 40, UsedBytes: 40 * gb, FreeBytes: 60 * gb, TotalBytes: 100 * gb}}})
	}
	minute.Close()
	quarter, _ := os.Create(filepath.Join(dir, "quarter.jsonl"))
	for i := 7 * 96; i >= 0; i-- {
		used := uint64((90 - 2*float64(i)/96) * gb)
		line(quarter, trends.Point{At: now.Add(-time.Duration(i) * 15 * time.Minute), Samples: 15, CPU: &cpu,
			Disks: map[string]trends.Disk{"/data": {UsedBytes: used, FreeBytes: 100*gb - used, TotalBytes: 100 * gb}}})
	}
	quarter.Close()
	return data
}

func TestTrendsJSONContract(t *testing.T) {
	data := writeTrends(t)
	code, out, _ := runCmd("trends", "--json", "--since", "30m", "--data", data)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	var v struct {
		APIVersion int               `json:"api_version"`
		Command    string            `json:"command"`
		OK         bool              `json:"ok"`
		Level      string            `json:"level"`
		Since      time.Time         `json:"since"`
		Points     []trends.Point    `json:"points"`
		Forecasts  []trends.Forecast `json:"forecasts"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if v.APIVersion != 1 || v.Command != "trends" || !v.OK || v.Level != "1m" || len(v.Points) < 30 || len(v.Points) > 31 {
		t.Fatalf("unexpected: level %s, %d points\n%s", v.Level, len(v.Points), out)
	}
	if len(v.Forecasts) != 1 || v.Forecasts[0].Disk != "/data" || v.Forecasts[0].DaysLeft < 4 || v.Forecasts[0].DaysLeft > 6 {
		t.Fatalf("forecast: %+v", v.Forecasts)
	}
}

func TestTrendsSaysWhatItShows(t *testing.T) {
	data := writeTrends(t)
	code, out, _ := runCmd("trends", "--since", "7d", "--data", data)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"Last 7d", "15m", "CPU", "/data", "fills up in about 5 days", "2.0 GB a day"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "—") {
		t.Error("no em dash in user-facing text")
	}
}

func TestTrendsBeforeAnyReading(t *testing.T) {
	code, out, _ := runCmd("trends", "--data", t.TempDir())
	if code != exitOK || !strings.Contains(out, "No readings yet") {
		t.Fatalf("exit %d: %s", code, out)
	}
	code, out, _ = runCmd("trends", "--json", "--data", t.TempDir())
	if code != exitOK || !strings.Contains(out, `"points": []`) || !strings.Contains(out, `"forecasts": []`) {
		t.Fatalf("empty lists, never null: %s", out)
	}
}

func TestTrendsRefusesASpanItDoesNotKeep(t *testing.T) {
	for _, since := range []string{"0m", "120d", "soon"} {
		if code, _, errOut := runCmd("trends", "--since", since, "--data", t.TempDir()); code != exitInvalid || errOut == "" {
			t.Errorf("--since %s: exit %d %q", since, code, errOut)
		}
	}
}
