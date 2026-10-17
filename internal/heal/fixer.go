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

// Fix does one fix and reports how it went. It never runs a shell.
func (f Fixer) Fix(ctx context.Context, m config.Monitor, p Plan) Outcome {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	var err error
	var output string
	switch {
	case p.Action == config.HealRestart && m.Type == "container":
		err = monitor.RestartContainer(ctx, m.Container)
	case p.Action == config.HealRestart && m.Type == "service":
		err = f.Platform.RestartService(ctx, m.Service)
	case p.Action == config.HealRun && len(p.Run) > 0:
		var code int
		code, output, err = monitor.RunProgram(ctx, f.Platform.PrepareCommand, p.Run, p.Timeout, f.OutputLimit)
		if err == nil && code != 0 {
			err = fmt.Errorf("%s exited with code %d%s", p.Run[0], code, tail(output))
		}
	default:
		err = errors.New("this monitor has no fix pulse can do")
	}
	o := Outcome{At: time.Now(), Err: err, Output: shorten(output, 500)}
	o.Fatal = err != nil && (errors.Is(err, host.ErrHealNotAllowed) || errors.Is(err, host.ErrNotFound) ||
		errors.Is(err, monitor.ErrNotAvailable) || errors.Is(err, host.ErrUnsupported))
	return o
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
