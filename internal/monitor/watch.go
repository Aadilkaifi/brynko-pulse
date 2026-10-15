// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Watcher is implemented by checkers that can tell when something changed,
// so the runner checks at once instead of waiting for the next interval.
// Watch runs until ctx ends.
type Watcher interface {
	Watch(ctx context.Context, changed func())
}

// Watch for service monitors: systemd signals on Linux, the Service Control Manager on Windows.
func (c *service) Watch(ctx context.Context, changed func()) {
	if c.events != nil {
		c.events.Watch(c.name, changed)
	}
}

// filePollEvery is how often file monitors look for a change. A stat is cheap,
// and it works the same on every filesystem, including network mounts.
const filePollEvery = 2 * time.Second

// Watch for file monitors: a change in existence, size or modification time.
func (c *file) Watch(ctx context.Context, changed func()) {
	last := fileStamp(c.path)
	t := time.NewTicker(filePollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if now := fileStamp(c.path); now != last {
				last = now
				changed()
			}
		}
	}
}

func fileStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	return fi.ModTime().UTC().Format(time.RFC3339Nano) + "/" + strconv.FormatInt(fi.Size(), 10)
}

// DockerEvents follows the Docker engine's event stream once for every
// container monitor.
type DockerEvents struct {
	log    *slog.Logger
	client *http.Client
	mu     sync.Mutex
	subs   map[string][]func()
	start  sync.Once
	ctx    context.Context
}

// NewDockerEvents prepares the stream; it connects when the first container
// monitor watches, and keeps reconnecting until ctx ends.
func NewDockerEvents(ctx context.Context, log *slog.Logger) *DockerEvents {
	return &DockerEvents{log: log, client: dockerClient(0), subs: map[string][]func(){}, ctx: ctx}
}

func (e *DockerEvents) watch(name string, changed func()) {
	if e == nil || runtime.GOOS == "windows" {
		return
	}
	e.mu.Lock()
	e.subs[name] = append(e.subs[name], changed)
	e.mu.Unlock()
	e.start.Do(func() { go e.run() })
}

func (e *DockerEvents) run() {
	backoff := time.Second
	for e.ctx.Err() == nil {
		if err := e.follow(); err != nil && e.ctx.Err() == nil {
			e.log.Debug("docker event stream ended; reconnecting", "error", err.Error(), "in", backoff.String())
		} else {
			backoff = time.Second
		}
		select {
		case <-e.ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (e *DockerEvents) follow() error {
	q := url.Values{"filters": {`{"type":["container"]}`}}
	req, err := http.NewRequestWithContext(e.ctx, http.MethodGet, "http://docker/events?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &url.Error{Op: "GET", URL: "/events", Err: errStatus(resp.Status)}
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var ev struct {
			Actor struct {
				ID         string
				Attributes map[string]string
			}
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		name := strings.TrimPrefix(ev.Actor.Attributes["name"], "/")
		e.mu.Lock()
		fns := append(append([]func(){}, e.subs[name]...), e.subs[ev.Actor.ID]...)
		e.mu.Unlock()
		for _, fn := range fns {
			fn()
		}
	}
	return sc.Err()
}

type errStatus string

func (s errStatus) Error() string { return "docker engine answered " + string(s) }

// Watch for container monitors: the container's Docker events.
func (c *container) Watch(_ context.Context, changed func()) {
	c.events.watch(c.name, changed)
}

// ListContainers names the running containers, for pulse discover. It
// returns nothing, without an error, where Docker is not installed.
func ListContainers(ctx context.Context) ([]string, error) {
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := dockerClient(10 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Docker (is this user in the docker group?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errStatus(resp.Status)
	}
	var list []struct{ Names []string }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return nil, err
	}
	var out []string
	for _, c := range list {
		if len(c.Names) > 0 {
			out = append(out, strings.TrimPrefix(c.Names[0], "/"))
		}
	}
	return out, nil
}
