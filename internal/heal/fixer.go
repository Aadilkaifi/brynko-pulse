// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package heal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/monitor"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// Fixer carries out fixes on this server.
type Fixer struct {
	Platform    platform.Platform
	OutputLimit int // bytes of a run fix's output kept
}

// Fix runs the plan's steps in order, stopping at the first that fails, and
// reports how it went. It never runs a shell.
func (f Fixer) Fix(ctx context.Context, p Plan) Outcome {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	var err error
	var output string
	if len(p.Steps) == 0 {
		err = errors.New("this monitor has no fix pulse can do")
	}
	for _, step := range p.Steps {
		var out string
		if out, err = f.step(ctx, step, p.Timeout); out != "" {
			output = out
		}
		if err != nil {
			break
		}
	}
	o := Outcome{At: time.Now(), Err: err, Output: shorten(output, 500)}
	o.Fatal = err != nil && (errors.Is(err, host.ErrHealNotAllowed) || errors.Is(err, host.ErrNotFound) ||
		errors.Is(err, monitor.ErrNotAvailable) || errors.Is(err, host.ErrUnsupported))
	return o
}

func (f Fixer) step(ctx context.Context, s config.HealStep, timeout time.Duration) (string, error) {
	for _, name := range s.RestartService {
		if err := f.Platform.RestartService(ctx, name); err != nil {
			return "", fmt.Errorf("restart %s: %w", name, err)
		}
	}
	for _, name := range s.RestartContainer {
		if err := monitor.RestartContainer(ctx, name); err != nil {
			return "", fmt.Errorf("restart the container %s: %w", name, err)
		}
	}
	if len(s.Run) == 0 {
		return "", nil
	}
	code, output, err := monitor.RunProgram(ctx, f.Platform.PrepareCommand, s.Run, timeout, f.OutputLimit)
	if err == nil && code != 0 {
		err = fmt.Errorf("%s exited with code %d%s", s.Run[0], code, tail(output))
	}
	return output, err
}

func tail(out string) string {
	if out == "" {
		return ""
	}
	return ": " + shorten(out, 200)
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
