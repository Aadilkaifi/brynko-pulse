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
	last := s.got[len(s.got)-1]
	if last.Kind != alert.KindSummary || !strings.Contains(last.Detail, "7 more alerts") {
		t.Fatalf("summary = %+v", last)
	}
}

func TestUnavailableChannelReported(t *testing.T) {
	d := New(&config.Config{ServerName: "x", Limits: config.Limits{MaxAlertsPerMinute: 5},
		Channels: map[string]config.Channel{"relay": {Type: "brynko"}}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if d.Status()["relay"].Unavailable == "" {
		t.Fatal("brynko channel should be reported as unavailable in this version")
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
