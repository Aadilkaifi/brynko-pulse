// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/store"
)

type fakeSender struct {
	mu       sync.Mutex
	got      []alert.Alert
	failures int
}

func (f *fakeSender) Send(_ context.Context, a alert.Alert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failures > 0 {
		f.failures--
		return errors.New("temporary failure")
	}
	f.got = append(f.got, a)
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

func newTest(limit int, senders map[string]*fakeSender, minSev map[string]string) *Dispatcher {
	d := New(&config.Config{ServerName: "web-1", Limits: config.Limits{MaxAlertsPerMinute: limit}}, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.retryDelay = []time.Duration{time.Millisecond, time.Millisecond}
	for id, s := range senders {
		d.targets[id] = target{sender: s, minSeverity: orDefault(minSev[id], "info")}
		d.status[id] = store.ChannelStatus{}
	}
	return d
}

func fire(sev string) alert.Alert {
	return alert.Alert{Server: "web-1", Monitor: "m", Kind: alert.KindFiring, Severity: sev}
}

func TestSeverityFilter(t *testing.T) {
	mail, tg := &fakeSender{}, &fakeSender{}
	d := newTest(10, map[string]*fakeSender{"mail": mail, "tg": tg}, map[string]string{"tg": "critical"})
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)
	d.Dispatch(fire("warning"), []string{"mail", "tg"})
	cancel()
	d.Stop(5 * time.Second)
	if mail.count() != 1 || tg.count() != 0 {
		t.Fatalf("mail=%d tg=%d, want 1 and 0", mail.count(), tg.count())
	}
}

func TestRetriesThenSucceeds(t *testing.T) {
	s := &fakeSender{failures: 2}
	d := newTest(10, map[string]*fakeSender{"mail": s}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)
	d.Dispatch(fire("critical"), []string{"mail"})
	time.Sleep(100 * time.Millisecond)
	cancel()
	d.Stop(5 * time.Second)
	if s.count() != 1 || d.Status()["mail"].LastError != "" {
		t.Fatalf("sent %d, status %+v", s.count(), d.Status()["mail"])
	}
}

func TestStormGuardHoldsAndSummarises(t *testing.T) {
	s := &fakeSender{}
	d := newTest(3, map[string]*fakeSender{"mail": s}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)
	for i := 0; i < 10; i++ {
		d.Dispatch(fire("critical"), []string{"mail"})
	}
	d.flushHeld()
	time.Sleep(100 * time.Millisecond)
	cancel()
	d.Stop(5 * time.Second)
	if s.count() != 4 {
		t.Fatalf("sent %d, want 3 alerts plus 1 summary", s.count())
	}
	// Workers deliver in parallel, so the summary is not always the last one sent.
	s.mu.Lock()
	defer s.mu.Unlock()
	var summaries []alert.Alert
	for _, a := range s.got {
		if a.Kind == alert.KindSummary {
			summaries = append(summaries, a)
		}
	}
	if len(summaries) != 1 || !strings.Contains(summaries[0].Detail, "7 more alerts") {
		t.Fatalf("summaries = %+v", summaries)
	}
}

func TestUnavailableChannelReported(t *testing.T) {
	d := New(&config.Config{ServerName: "x", Limits: config.Limits{MaxAlertsPerMinute: 5},
		Channels: map[string]config.Channel{"relay": {Type: "carrier-pigeon"}}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if d.Status()["relay"].Unavailable == "" {
		t.Fatal("a channel that cannot be built should be reported as unavailable")
	}
	if err := d.SendNow(context.Background(), "relay", fire("info")); err == nil {
		t.Fatal("SendNow to an unavailable channel must fail")
	}
	if err := d.SendNow(context.Background(), "nope", fire("info")); err == nil || !strings.Contains(err.Error(), "no channel named") {
		t.Fatalf("unknown channel: %v", err)
	}
}

func TestDispatchAfterStopIsSafe(t *testing.T) {
	d := newTest(10, map[string]*fakeSender{"mail": {}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)
	cancel()
	d.Stop(time.Second)
	if got := d.Dispatch(fire("critical"), []string{"mail"}); got != nil {
		t.Fatal("dispatch after stop must not queue")
	}
	d.flushHeld()
}

// A channel that still fails after every retry is reported once, with its error, so the
// history can say the alert did not arrive. One that recovers on a retry is not reported.
func TestFailureReportedAfterRetries(t *testing.T) {
	bad, flaky := &fakeSender{failures: 99}, &fakeSender{failures: 1}
	d := newTest(10, map[string]*fakeSender{"mail": bad, "tg": flaky}, nil)
	var mu sync.Mutex
	var got []Failure
	d.OnFailure(func(f Failure) { mu.Lock(); got = append(got, f); mu.Unlock() })
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)
	a := fire("critical")
	a.At = time.Date(2026, 10, 17, 20, 23, 20, 0, time.UTC)
	d.Dispatch(a, []string{"mail", "tg"})
	time.Sleep(100 * time.Millisecond)
	cancel()
	d.Stop(5 * time.Second)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Channel != "mail" || got[0].Err != "temporary failure" || !got[0].Alert.At.Equal(a.At) {
		t.Fatalf("failures = %+v", got)
	}
	if flaky.count() != 1 {
		t.Fatalf("tg sent %d, want 1", flaky.count())
	}
}

// A reload keeps the last error of a channel it did not change, so the warning stays; a
// changed channel, or changed secrets, start fresh.
func TestReloadKeepsStatusOfUnchangedChannels(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mail := config.Channel{Type: "webhook", URL: "https://example.invalid/a"}
	hook := config.Channel{Type: "webhook", URL: "https://example.invalid/b"}
	cfg := func(m, h config.Channel, sec config.Secrets) *config.Config {
		return &config.Config{Limits: config.Limits{MaxAlertsPerMinute: 5}, Secrets: sec,
			Channels: map[string]config.Channel{"mail": m, "hook": h}}
	}
	broken := func(d *Dispatcher) {
		for _, id := range []string{"mail", "hook"} {
			st := d.status[id]
			st.LastError, st.LastErrorAt = "no such host", time.Now()
			d.status[id] = st
		}
	}
	prev := New(cfg(mail, hook, nil), nil, log)
	broken(prev)
	fixed := hook
	fixed.URL = "https://example.invalid/c"
	next := New(cfg(mail, fixed, nil), nil, log)
	next.KeepStatus(prev)
	if next.Status()["mail"].LastError != "no such host" || next.Status()["hook"].LastError != "" {
		t.Fatalf("status = %+v", next.Status())
	}
	other := New(cfg(mail, hook, config.Secrets{"X": "1"}), nil, log)
	other.KeepStatus(prev)
	if other.Status()["mail"].LastError != "" {
		t.Fatalf("changed secrets must start fresh: %+v", other.Status())
	}
}

// After a restart the saved health comes back for channels of the same type only.
func TestRestoreSavedStatus(t *testing.T) {
	d := New(&config.Config{Limits: config.Limits{MaxAlertsPerMinute: 5}, Channels: map[string]config.Channel{
		"mail": {Type: "webhook", URL: "https://example.invalid/a"}, "hook": {Type: "webhook", URL: "https://example.invalid/b"}}},
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	at := time.Date(2026, 10, 17, 20, 23, 32, 0, time.UTC)
	d.Restore(map[string]store.ChannelStatus{
		"mail": {Type: "webhook", LastError: "no such host", LastErrorAt: at},
		"hook": {Type: "smtp", LastError: "was an email channel before"},
		"gone": {Type: "webhook", LastError: "removed"},
	})
	st := d.Status()
	if st["mail"].LastError != "no such host" || !st["mail"].LastErrorAt.Equal(at) || st["hook"].LastError != "" || len(st) != 2 {
		t.Fatalf("status = %+v", st)
	}
}
