// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package linux

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

const (
	systemdBus   = "org.freedesktop.systemd1"
	systemdPath  = "/org/freedesktop/systemd1"
	unitPathRoot = "/org/freedesktop/systemd1/unit"
)

// serviceEvents listens to systemd over D-Bus. One connection serves every
// service monitor.
type serviceEvents struct {
	log  *slog.Logger
	mu   sync.Mutex
	subs map[dbus.ObjectPath][]func()
}

// NewServiceWatcher connects to systemd and keeps reconnecting until ctx ends.
// It fails only when there is no system bus at all, for example in a container.
func (Platform) NewServiceWatcher(ctx context.Context, log *slog.Logger) (host.ServiceWatcher, error) {
	conn, err := dialSystemd(ctx)
	if err != nil {
		return nil, err
	}
	e := &serviceEvents{log: log, subs: map[dbus.ObjectPath][]func(){}}
	go e.run(ctx, conn)
	return e, nil
}

func (e *serviceEvents) Watch(name string, changed func()) {
	path := busPath(unitName(name))
	e.mu.Lock()
	e.subs[path] = append(e.subs[path], changed)
	e.mu.Unlock()
}

func dialSystemd(ctx context.Context) (*dbus.Conn, error) {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("connect to the system bus: %w", err)
	}
	// systemd only sends unit signals to clients that subscribed.
	if err := conn.Object(systemdBus, systemdPath).CallWithContext(ctx, systemdBus+".Manager.Subscribe", 0).Err; err != nil {
		conn.Close()
		return nil, fmt.Errorf("subscribe to systemd: %w", err)
	}
	if err := conn.AddMatchSignalContext(ctx, dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchPathNamespace(unitPathRoot)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("listen for systemd changes: %w", err)
	}
	return conn, nil
}

func (e *serviceEvents) run(ctx context.Context, conn *dbus.Conn) {
	backoff := time.Second
	for {
		signals := make(chan *dbus.Signal, 64)
		conn.Signal(signals)
		e.dispatch(ctx, signals)
		conn.Close()
		if ctx.Err() != nil {
			return
		}
		e.log.Warn("lost the connection to systemd; reconnecting", "in", backoff.String())
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			var err error
			if conn, err = dialSystemd(ctx); err == nil {
				backoff = time.Second
				break
			}
			backoff = min(backoff*2, 30*time.Second)
		}
	}
}

// dispatch returns when ctx ends or the connection closes the channel.
func (e *serviceEvents) dispatch(ctx context.Context, signals <-chan *dbus.Signal) {
	for {
		select {
		case <-ctx.Done():
			return
		case s, ok := <-signals:
			if !ok {
				return
			}
			if !activeStateChanged(s) {
				continue
			}
			e.mu.Lock()
			fns := e.subs[s.Path]
			e.mu.Unlock()
			for _, fn := range fns {
				fn()
			}
		}
	}
}

// activeStateChanged keeps only the Unit interface's state changes; systemd
// also reports timers and resource counters that change all the time.
func activeStateChanged(s *dbus.Signal) bool {
	if len(s.Body) < 2 {
		return false
	}
	if iface, _ := s.Body[0].(string); iface != systemdBus+".Unit" {
		return false
	}
	props, _ := s.Body[1].(map[string]dbus.Variant)
	_, active := props["ActiveState"]
	_, sub := props["SubState"]
	return active || sub
}

func unitName(name string) string {
	if strings.Contains(name, ".") {
		return name
	}
	return name + ".service"
}

// busPath escapes a unit name the way systemd names its D-Bus objects.
func busPath(unit string) dbus.ObjectPath {
	var b strings.Builder
	for i := 0; i < len(unit); i++ {
		c := unit[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || (c >= '0' && c <= '9' && i > 0) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "_%02x", c)
		}
	}
	return dbus.ObjectPath(unitPathRoot + "/" + b.String())
}

// FollowEventLog is for Windows; on Linux use a file or the journal.
func (Platform) FollowEventLog(context.Context, string, func(string)) error {
	return fmt.Errorf("event log sources: %w; use file or journal", host.ErrUnsupported)
}
