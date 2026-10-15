// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
)

type file struct{ path string }

func (c *file) Check(context.Context) (rule.Values, error) {
	fi, err := os.Stat(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s does not exist", c.path)
	}
	if err != nil {
		return nil, err
	}
	return rule.Values{
		"exists":     true,
		"age":        time.Since(fi.ModTime()).Seconds(),
		"size_bytes": float64(fi.Size()),
	}, nil
}

type command struct {
	run     []string
	parse   string
	timeout time.Duration
	limit   int
}

func newCommand(m config.Monitor, limit int) Checker {
	return &command{run: m.Run, parse: or(m.Parse, "exit_code"), timeout: orDur(m.Timeout, 30*time.Second), limit: limit}
}

// limitedBuffer keeps the first n bytes and discards the rest.
type limitedBuffer struct {
	buf bytes.Buffer
	n   int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.n - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *command) Check(ctx context.Context) (rule.Values, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	// The argument list goes straight to the program: there is no shell to inject into.
	cmd := exec.CommandContext(ctx, c.run[0], c.run[1:]...)
	out := &limitedBuffer{n: c.limit}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("%s did not finish within %v", c.run[0], c.timeout)
	}
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		return nil, fmt.Errorf("cannot run %s: %w", c.run[0], err)
	}
	text := strings.TrimSpace(out.buf.String())
	v := rule.Values{"exit_code": float64(code)}
	switch c.parse {
	case "text":
		v["text"] = text
	case "number":
		first := strings.Fields(text)
		if len(first) == 0 {
			return nil, fmt.Errorf("%s printed nothing, expected a number", c.run[0])
		}
		n, err := strconv.ParseFloat(first[0], 64)
		if err != nil {
			return nil, fmt.Errorf("%s printed %q, expected a number", c.run[0], first[0])
		}
		v["value"] = n
	}
	return v, nil
}

// heartbeat is late when nothing has called `pulse ping <id>` in time.
type heartbeat struct {
	dir, id      string
	every, grace time.Duration
	started      time.Time
}

// HeartbeatFile is where `pulse ping` records a heartbeat.
func HeartbeatFile(dataDir, id string) string {
	return filepath.Join(dataDir, "heartbeats", id)
}

func (c *heartbeat) Check(context.Context) (rule.Values, error) {
	last := c.started
	if fi, err := os.Stat(HeartbeatFile(c.dir, c.id)); err == nil && fi.ModTime().After(last) {
		last = fi.ModTime()
	}
	age := time.Since(last)
	return rule.Values{"age": age.Seconds(), "late": age > c.every+c.grace}, nil
}

// container asks the local Docker engine about one container.
type container struct {
	name   string
	client *http.Client
	events *DockerEvents
}

func newContainer(name string, events *DockerEvents) Checker {
	return &container{name: name, client: dockerClient(10 * time.Second), events: events}
}

// dockerClient talks to the local Docker engine over its socket. A zero
// timeout is for the event stream, which stays open.
func dockerClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", "/var/run/docker.sock")
	}}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func (c *container) Check(ctx context.Context) (rule.Values, error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("container monitors on Windows: %w yet", ErrNotAvailable)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+urlPathEscape(c.name)+"/json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Docker (is the pulse user in the docker group?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("container %s does not exist", c.name)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker engine answered %s", resp.Status)
	}
	var info struct {
		RestartCount int
		State        struct {
			Status    string
			Running   bool
			ExitCode  int
			OOMKilled bool
			Health    *struct{ Status string }
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("read Docker answer: %w", err)
	}
	v := rule.Values{
		"running":    info.State.Running,
		"state":      info.State.Status,
		"restarts":   float64(info.RestartCount),
		"exit_code":  float64(info.State.ExitCode),
		"oom_killed": info.State.OOMKilled,
	}
	if info.State.Health != nil {
		v["health"] = info.State.Health.Status
	}
	return v, nil
}

func urlPathEscape(s string) string {
	return strings.NewReplacer("/", "%2F", "?", "%3F", "#", "%23").Replace(s)
}
