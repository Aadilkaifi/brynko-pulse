// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package trends

import (
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// fakeSource reads like a machine whose CPU runs at `busy` percent and whose disk grows.
type fakeSource struct {
	ticks    uint64
	idle     uint64
	busy     float64
	used     uint64
	total    uint64
	noLoad   bool
	diskFail bool
}

// CPUTimes counts up like the real counters: each minute adds 1000 ticks, `busy` percent of them busy.
func (f *fakeSource) CPUTimes() (host.CPUTimes, error) {
	f.ticks += 1000
	f.idle += 1000 - uint64(f.busy*10)
	return host.CPUTimes{Total: f.ticks, Idle: f.idle}, nil
}

func (f *fakeSource) Memory() (host.Memory, error) {
	return host.Memory{TotalBytes: 8 << 30, AvailableBytes: 2 << 30, SwapTotalBytes: 2 << 30, SwapFreeBytes: 1 << 30}, nil
}

func (f *fakeSource) LoadAverage() (host.Load, error) {
	if f.noLoad {
		return host.Load{}, host.ErrUnsupported
	}
	return host.Load{Load1: 0.5, Load5: 0.4, Load15: 0.3, CPUs: 2}, nil
}

func (f *fakeSource) DiskUsage(string) (host.Disk, error) {
	if f.diskFail {
		return host.Disk{}, errors.New("not mounted")
	}
	return host.Disk{TotalBytes: f.total, UsedBytes: f.used, FreeBytes: f.total - f.used}, nil
}

func newRec(t *testing.T, src Source) (*Recorder, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := NewRecorder(dir, src, func() []string { return []string{"/"} }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

var t0 = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

func TestAReadingHasEveryValueTheSystemGives(t *testing.T) {
	src := &fakeSource{busy: 25, used: 40 << 30, total: 100 << 30}
	r, dir := newRec(t, src)
	for i := range 2 { // CPU needs two readings
		if err := r.Record(t0.Add(time.Duration(i) * time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	pts, err := Read(dir, Levels[0], t0)
	if err != nil || len(pts) != 2 {
		t.Fatalf("points %v, %v", pts, err)
	}
	if pts[0].CPU != nil {
		t.Error("the first reading has no CPU: it needs a previous one")
	}
	p := pts[1]
	if p.CPU == nil || math.Abs(*p.CPU-25) > 0.5 {
		t.Errorf("cpu %v, want about 25", p.CPU)
	}
	if *p.Mem != 75 || *p.Swap != 50 || *p.Load1 != 0.5 || p.MemUsed != 6<<30 {
		t.Errorf("mem %v swap %v load %v used %v", *p.Mem, *p.Swap, *p.Load1, p.MemUsed)
	}
	if d := p.Disks["/"]; d.UsedPct != 40 || d.FreeBytes != 60<<30 {
		t.Errorf("disk %+v", d)
	}
}

func TestAValueTheSystemCannotGiveIsLeftOut(t *testing.T) {
	r, dir := newRec(t, &fakeSource{noLoad: true, diskFail: true, total: 1})
	if err := r.Record(t0); err != nil {
		t.Fatal(err)
	}
	pts, _ := Read(dir, Levels[0], t0)
	if pts[0].Load1 != nil || len(pts[0].Disks) != 0 {
		t.Errorf("load and an unreadable disk are left out: %+v", pts[0])
	}
}

func TestAveragesAreWrittenWhenTheirTimeEnds(t *testing.T) {
	src := &fakeSource{busy: 10, used: 10 << 30, total: 100 << 30}
	r, dir := newRec(t, src)
	for i := range 136 { // 10:00 to 12:15: closes the nine 15-minute averages from 10:00 to 12:00, and the 10:00 and 11:00 hours
		if i == 30 {
			src.busy = 50
		}
		if err := r.Record(t0.Add(time.Duration(i) * time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	quarters, _ := Read(dir, Levels[1], t0)
	if len(quarters) != 9 || !quarters[0].At.Equal(t0) || quarters[0].Samples != 15 {
		t.Fatalf("15-minute averages: %d, first %+v", len(quarters), quarters[0])
	}
	if *quarters[2].CPU < 49 || *quarters[2].CPUMax < 49 {
		t.Errorf("10:30 average %v max %v", *quarters[2].CPU, *quarters[2].CPUMax)
	}
	hours, _ := Read(dir, Levels[2], t0)
	if len(hours) != 2 || hours[0].Samples != 60 || !hours[1].At.Equal(t0.Add(time.Hour)) {
		t.Fatalf("hourly averages: %+v", hours)
	}
	if *hours[0].CPUMax < 49 || *hours[0].CPU > 40 {
		t.Errorf("the hour keeps its peak and averages the rest: avg %v max %v", *hours[0].CPU, *hours[0].CPUMax)
	}
}

func TestARestartKeepsTheAverageThatWasOpen(t *testing.T) {
	src := &fakeSource{busy: 20, used: 1, total: 10}
	r, dir := newRec(t, src)
	for i := range 7 {
		_ = r.Record(t0.Add(time.Duration(i) * time.Minute))
	}
	// pulse restarts at 10:07; NewRecorder reloads 10:00..10:06 for the open 15-minute average
	r2, err := NewRecorder(dir, src, func() []string { return []string{"/"} }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r2.mu.Lock()
	open := len(r2.pending["15m"])
	r2.mu.Unlock()
	if open == 0 && time.Now().Truncate(15*time.Minute).Equal(t0) {
		t.Errorf("no readings reloaded")
	}
	for i := 7; i < 16; i++ {
		_ = r2.Record(t0.Add(time.Duration(i) * time.Minute))
	}
	quarters, _ := Read(dir, Levels[1], t0)
	if len(quarters) != 1 {
		t.Fatalf("one 15-minute average, got %d", len(quarters))
	}
}

func TestOldPointsArePrunedHourly(t *testing.T) {
	src := &fakeSource{used: 1, total: 10}
	r, dir := newRec(t, src)
	_ = r.Record(t0)
	r.pruned = time.Time{}
	if err := r.Record(t0.Add(25 * time.Hour)); err != nil { // the first reading is now older than a day
		t.Fatal(err)
	}
	pts, _ := Read(dir, Levels[0], time.Time{})
	if len(pts) != 1 || !pts[0].At.Equal(t0.Add(25*time.Hour)) {
		t.Errorf("minute readings older than a day are dropped: %v", pts)
	}
}

func TestTheLevelFollowsTheSpanAsked(t *testing.T) {
	for span, want := range map[time.Duration]string{
		time.Hour: "1m", 24 * time.Hour: "1m", 7 * 24 * time.Hour: "15m", 14 * 24 * time.Hour: "15m", 30 * 24 * time.Hour: "1h", 400 * 24 * time.Hour: "1h",
	} {
		if got := LevelFor(span).Name; got != want {
			t.Errorf("%v: %s, want %s", span, got, want)
		}
	}
}

// ── forecasts ──

func growth(days int, perDay float64, start uint64, total uint64, noise func(i int) float64) []Point {
	var pts []Point
	for i := 0; i <= days*24; i++ {
		used := float64(start) + perDay*float64(i)/24
		if noise != nil {
			used += noise(i)
		}
		u := uint64(used)
		pts = append(pts, Point{At: t0.Add(time.Duration(i) * time.Hour),
			Disks: map[string]Disk{"/data": {UsedBytes: u, FreeBytes: total - u, TotalBytes: total}}})
	}
	return pts
}

func TestASteadilyGrowingDiskGetsADate(t *testing.T) {
	const gb = 1 << 30
	pts := growth(5, 2*gb, 80*gb, 100*gb, nil) // 2 GB a day from 80 GB: 90 GB after 5 days, 10 GB free
	now := pts[len(pts)-1].At
	fc := DiskForecasts(pts, now)
	if len(fc) != 1 {
		t.Fatalf("forecasts %+v", fc)
	}
	f := fc[0]
	if f.Disk != "/data" || math.Abs(f.DaysLeft-5) > 0.2 || f.Confidence < 0.99 || float64(f.GrowthPerDay) < 1.9*gb {
		t.Errorf("%+v", f)
	}
	if want := now.Add(5 * 24 * time.Hour); f.FullAt.Sub(want).Abs() > 6*time.Hour {
		t.Errorf("full at %v, want about %v", f.FullAt, want)
	}
}

func TestNoGuessWithoutSteadyGrowthOrEnoughHistory(t *testing.T) {
	const gb = 1 << 30
	jumpy := growth(5, 0.1*gb, 50*gb, 100*gb, func(i int) float64 { return float64((i*37)%11) * gb })
	if fc := DiskForecasts(jumpy, jumpy[len(jumpy)-1].At); len(fc) != 0 {
		t.Errorf("noisy disk: %+v", fc)
	}
	short := growth(5, 2*gb, 80*gb, 100*gb, nil)[:10]
	if fc := DiskForecasts(short, short[len(short)-1].At); len(fc) != 0 {
		t.Errorf("under a day of history: %+v", fc)
	}
	slow := growth(5, 0.1*gb, 10*gb, 100*gb, nil) // 900 days away
	if fc := DiskForecasts(slow, slow[len(slow)-1].At); len(fc) != 0 {
		t.Errorf("too far ahead: %+v", fc)
	}
	shrinking := growth(5, -1*gb, 80*gb, 100*gb, nil)
	if fc := DiskForecasts(shrinking, shrinking[len(shrinking)-1].At); len(fc) != 0 {
		t.Errorf("shrinking disk: %+v", fc)
	}
}

func TestTheTrendStartsAfterACleanup(t *testing.T) {
	const gb = 1 << 30
	pts := growth(6, 2*gb, 80*gb, 100*gb, nil)
	// at day 3 someone freed 30 GB; growth carries on from there
	for i := 72; i < len(pts); i++ {
		d := pts[i].Disks["/data"]
		d.UsedBytes -= 30 * gb
		d.FreeBytes += 30 * gb
		pts[i].Disks["/data"] = d
	}
	fc := DiskForecasts(pts, pts[len(pts)-1].At)
	if len(fc) != 1 || fc[0].BasedOnDays > 3.1 || math.Abs(fc[0].DaysLeft-19) > 0.5 {
		t.Errorf("only the days after the cleanup count: %+v", fc)
	}
}
