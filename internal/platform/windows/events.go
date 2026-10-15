// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// pollEvery is how often watched services are asked for their state. A query
// to the Service Control Manager is cheap, so this stays well under the usual
// monitor interval without loading the system.
const pollEvery = 2 * time.Second

type serviceEvents struct {
	p    Platform
	mu   sync.Mutex
	subs map[string][]func()
	last map[string]string
}

// NewServiceWatcher watches services by asking the Service Control Manager
// every two seconds and calling back on any change of state.
func (p Platform) NewServiceWatcher(ctx context.Context, _ *slog.Logger) (host.ServiceWatcher, error) {
	e := &serviceEvents{p: p, subs: map[string][]func(){}, last: map[string]string{}}
	go e.run(ctx)
	return e, nil
}

func (e *serviceEvents) Watch(name string, changed func()) {
	key := strings.ToLower(name)
	e.mu.Lock()
	e.subs[key] = append(e.subs[key], changed)
	e.mu.Unlock()
}

func (e *serviceEvents) run(ctx context.Context) {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		e.mu.Lock()
		names := make([]string, 0, len(e.subs))
		for n := range e.subs {
			names = append(names, n)
		}
		e.mu.Unlock()
		for _, n := range names {
			state := "missing"
			if s, err := e.p.Service(ctx, n); err == nil {
				state = s.State
			}
			e.mu.Lock()
			prev, seen := e.last[n]
			e.last[n] = state
			fns := e.subs[n]
			e.mu.Unlock()
			if seen && prev != state {
				for _, fn := range fns {
					fn()
				}
			}
		}
	}
}
