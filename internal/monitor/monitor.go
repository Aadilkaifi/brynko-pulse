// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package monitor measures one thing on the server. Every monitor type
// implements Checker; the engine decides what the measurements mean.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
)

// ErrNotReady means the check needs another sample before it can report,
// for example CPU usage, which is measured between two readings.
var ErrNotReady = errors.New("collecting a first sample")

// ErrNotAvailable means this monitor type is not available in this version or on this OS.
var ErrNotAvailable = errors.New("not available")

// Checker measures one monitor.
type Checker interface {
	Check(ctx context.Context) (rule.Values, error)
}

// Deps are what monitors need from the outside world.
type Deps struct {
	Platform    platform.Platform
	DataDir     string
	OutputLimit int
	Started     time.Time

	// Event sources shared by every monitor of one configuration. Nil means
	// checks happen only at each interval.
	Services host.ServiceWatcher
	Docker   *DockerEvents
	Log      *slog.Logger
}

// New builds the checker for m, and the alert condition to use when m sets none
// (the type's default in Catalog).
func New(m config.Monitor, d Deps) (Checker, string, error) {
	switch m.Type {
	case "disk":
		return &disk{p: d.Platform, path: m.Path}, "", nil
	case "memory":
		return &memory{p: d.Platform}, "", nil
	case "swap":
		return &swap{p: d.Platform}, "", nil
	case "cpu":
		return &cpu{p: d.Platform}, "", nil
	case "load":
		return &load{p: d.Platform}, "", nil
	case "service":
		return &service{p: d.Platform, name: m.Service, events: d.Services}, defaultCondition(m.Type), nil
	case "process":
		return &process{p: d.Platform, match: m.Match}, defaultCondition(m.Type), nil
	case "container":
		return newContainer(m.Container, d.Docker), defaultCondition(m.Type), nil
	case "port":
		return &port{host: or(m.Host, "127.0.0.1"), port: m.Port, timeout: orDur(m.Timeout, 5*time.Second)}, defaultCondition(m.Type), nil
	case "http":
		c, err := newHTTP(m)
		return c, defaultCondition(m.Type), err
	case "ssl":
		return &ssl{host: m.Host, file: m.File, timeout: orDur(m.Timeout, 10*time.Second)}, defaultCondition(m.Type), nil
	case "file":
		return &file{path: m.Path}, "", nil
	case "command":
		return newCommand(m, d), defaultCondition(m.Type), nil
	case "heartbeat":
		return &heartbeat{dir: d.DataDir, id: m.ID, every: m.ExpectEvery.D(), grace: orDur(m.Grace, time.Minute),
			started: d.Started}, defaultCondition(m.Type), nil
	case "log":
		c, err := newLog(m, d)
		return c, defaultCondition(m.Type), err
	default:
		return nil, "", fmt.Errorf("unknown monitor type %q", m.Type)
	}
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func orDur(d config.Duration, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d.D()
}

func percent(part, whole uint64) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}
