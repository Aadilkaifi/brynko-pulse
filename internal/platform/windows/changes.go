// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

// ChangeSources reads the System and Application event logs for services installed, restarts
// someone started, software installed or removed and Windows updates.
func (p Platform) ChangeSources(_ *slog.Logger) []host.ChangeSource {
	return []host.ChangeSource{eventLogChanges{p: p}}
}

// ConfigFiles are the hosts file and the IIS configuration.
func (Platform) ConfigFiles() []string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return []string{
		filepath.Join(root, `System32\drivers\etc\hosts`),
		filepath.Join(root, `System32\inetsrv\config\applicationHost.config`),
	}
}

type eventLogChanges struct{ p Platform }

func (eventLogChanges) Name() string { return "eventlog" }

// Follow reads events from now on; the Event Log keeps its own history, and changes made while pulse
// was stopped are not read back. The cursor is not used.
func (e eventLogChanges) Follow(ctx context.Context, _ string, emit func(host.Change), _ func(string)) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, channel := range []string{"System", "Application"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := e.p.FollowEventLog(ctx, channel, func(line string) {
				if c, ok := host.ParseWindowsChange(time.Now().UTC(), line); ok {
					emit(c)
				}
			})
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}
