// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package notify routes alerts to channels: severity filtering, retries, and a
// storm guard that folds a flood of alerts into one summary.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/alert"
	"github.com/Aadilkaifi/brynko-pulse/internal/channel"
	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/store"
)

const (
	queueSize   = 1000
	workers     = 4
	sendTimeout = 30 * time.Second
)

var retryDelays = []time.Duration{2 * time.Second, 10 * time.Second}

type target struct {
	sender      channel.Sender
	minSeverity string
	typ         string
}

type job struct {
	a   alert.Alert
	ids []string
}

// Dispatcher is safe for concurrent use.
type Dispatcher struct {
	server  string
	address string // server_address, shown beside the name
	targets map[string]target
	limit   int
	log     *slog.Logger
	now     func() time.Time
	queue   chan job
	wg      sync.WaitGroup

	mu         sync.Mutex
	closed     bool
	window     []time.Time
	held       map[string]int // channel -> alerts held back by the storm guard
	heldWorst  string
	status     map[string]store.ChannelStatus
	retryDelay []time.Duration
	onFail     func(Failure)
}

// Failure is an alert that could not be delivered to one channel, after every retry.
type Failure struct {
	Channel string
	Alert   alert.Alert
	Err     string // the channel's error; channels never put a URL or a secret in it
}

// OnFailure sets fn to be called for every alert a channel could not deliver, so the
// history can show it. Set it before Start; fn runs on a delivery worker.
func (d *Dispatcher) OnFailure(fn func(Failure)) {
	d.onFail = fn
}

// New builds a sender for every configured channel. Channels that cannot be
// built are reported in Status and skipped, so one bad channel never stops the rest.
func New(cfg *config.Config, client *http.Client, log *slog.Logger) *Dispatcher {
	d := &Dispatcher{
		server: cfg.ServerName, address: cfg.ServerAddress, targets: map[string]target{}, limit: cfg.Limits.MaxAlertsPerMinute,
		log: log, now: time.Now, queue: make(chan job, queueSize), held: map[string]int{},
		status: map[string]store.ChannelStatus{}, retryDelay: retryDelays,
	}
	for id, c := range cfg.Channels {
		st := store.ChannelStatus{Type: c.Type}
		s, err := channel.New(c, cfg.Secrets, client)
		if err != nil {
			st.Unavailable = err.Error()
			log.Warn("channel cannot be used", "channel", id, "reason", err.Error())
		} else {
			d.targets[id] = target{sender: s, minSeverity: orDefault(c.MinSeverity, "info"), typ: c.Type}
		}
		d.status[id] = st
	}
	return d
}

// Start runs the delivery workers and the storm summary until ctx ends.
func (d *Dispatcher) Start(ctx context.Context) {
	for i := 0; i < workers; i++ {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			for j := range d.queue {
				d.deliver(ctx, j)
			}
		}()
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				d.flushHeld()
			}
		}
	}()
}

// Stop delivers what is queued, waiting at most timeout. Call it after the
// context passed to Start is done.
func (d *Dispatcher) Stop(timeout time.Duration) {
	d.mu.Lock()
	d.closed = true
	close(d.queue)
	d.mu.Unlock()
	done := make(chan struct{})
	go func() { d.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		d.log.Warn("stopped before every alert was delivered")
	}
}

// Dispatch queues a for the given channels. It returns the channels it will
// try, so history can record them.
func (d *Dispatcher) Dispatch(a alert.Alert, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	d.mu.Lock()
	now := d.now()
	cut := now.Add(-time.Minute)
	kept := d.window[:0]
	for _, t := range d.window {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	d.window = kept
	if len(d.window) >= d.limit {
		for _, id := range ids {
			d.held[id]++
		}
		if alert.AtLeast(a.Severity, d.heldWorst) || d.heldWorst == "" {
			d.heldWorst = a.Severity
		}
		d.mu.Unlock()
		d.log.Warn("alert held back by the storm guard", "monitor", a.Monitor)
		return nil
	}
	d.window = append(d.window, now)
	queued := d.enqueueLocked(job{a: a, ids: ids})
	d.mu.Unlock()
	if !queued {
		d.log.Error("alert not queued: the queue is full or pulse is stopping", "monitor", a.Monitor)
		return nil
	}
	return ids
}

// enqueueLocked never blocks, and never sends once Stop has closed the queue.
func (d *Dispatcher) enqueueLocked(j job) bool {
	if d.closed {
		return false
	}
	select {
	case d.queue <- j:
		return true
	default:
		return false
	}
}

func (d *Dispatcher) flushHeld() {
	d.mu.Lock()
	defer d.mu.Unlock()
	held, worst := d.held, d.heldWorst
	d.held, d.heldWorst = map[string]int{}, ""
	if len(held) == 0 {
		return
	}
	ids := make([]string, 0, len(held))
	total := 0
	for id, n := range held {
		ids = append(ids, id)
		total = max(total, n)
	}
	sort.Strings(ids)
	a := alert.Alert{Server: d.server, ServerAddress: d.address, Kind: alert.KindSummary, Severity: worst, At: d.now(),
		Detail: fmt.Sprintf("%d more alerts were held back in the last minute so you are not flooded. "+
			"Run pulse status on %s to see everything that is failing.", total, d.server)}
	d.enqueueLocked(job{a: a, ids: ids})
}

func (d *Dispatcher) deliver(ctx context.Context, j job) {
	for _, id := range j.ids {
		t, ok := d.targets[id]
		if !ok {
			continue
		}
		if j.a.Kind != alert.KindSummary && !alert.AtLeast(j.a.Severity, t.minSeverity) {
			continue
		}
		err := d.sendWithRetry(ctx, t.sender, j.a)
		d.mu.Lock()
		st := d.status[id]
		if err != nil {
			st.LastError, st.LastErrorAt = err.Error(), d.now()
		} else {
			st.LastSent, st.LastError = d.now(), ""
		}
		d.status[id] = st
		d.mu.Unlock()
		if err != nil {
			d.log.Error("alert not delivered", "channel", id, "monitor", j.a.Monitor, "error", err.Error())
			if d.onFail != nil {
				d.onFail(Failure{Channel: id, Alert: j.a, Err: err.Error()})
			}
		}
	}
}

func (d *Dispatcher) sendWithRetry(ctx context.Context, s channel.Sender, a alert.Alert) error {
	var err error
	for attempt := 0; ; attempt++ {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
		err = s.Send(sctx, a)
		cancel()
		if err == nil || attempt >= len(d.retryDelay) {
			return err
		}
		select {
		case <-time.After(d.retryDelay[attempt]):
		case <-ctx.Done():
			return err
		}
	}
}

// SendNow delivers a to one channel immediately, ignoring filters. Used by pulse test.
func (d *Dispatcher) SendNow(ctx context.Context, id string, a alert.Alert) error {
	t, ok := d.targets[id]
	if !ok {
		if st, known := d.status[id]; known && st.Unavailable != "" {
			return fmt.Errorf("channel %s cannot be used: %s", id, st.Unavailable)
		}
		return fmt.Errorf("there is no channel named %s in channels.yaml", id)
	}
	return t.sender.Send(ctx, a)
}

// Status returns the delivery health of every channel.
func (d *Dispatcher) Status() map[string]store.ChannelStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]store.ChannelStatus, len(d.status))
	for k, v := range d.status {
		out[k] = v
	}
	return out
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
