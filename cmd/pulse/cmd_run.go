// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/runner"
	"github.com/Aadilkaifi/brynko-pulse/internal/version"
)

func cmdRun(args []string, stdout, stderr io.Writer) int {
	p := platform.Current()
	fs := newFlags("run", stderr)
	cfgDir := fs.String("config", p.ConfigDir(), "configuration directory")
	dataDir := fs.String("data", p.DataDir(), "where state and alert history are kept")
	if fs.Parse(args) != nil {
		return exitUsage
	}

	opts := runner.Options{ConfigDir: *cfgDir, DataDir: *dataDir, Platform: p, Version: version.Get().Version}

	// Under the Windows Service Control Manager, it owns stop and reload.
	ran, err := p.RunAsService(func(ctx context.Context, reload <-chan struct{}, log *slog.Logger) error {
		o := opts
		o.Log, o.Reload = log, reload
		return runner.Run(ctx, o)
	})
	if ran || err != nil {
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitInvalid
		}
		return exitOK
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts.Log = slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	opts.Reload = hangups(ctx)
	if err := runner.Run(ctx, opts); err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	return exitOK
}

// hangups turns SIGHUP into reload requests. SIGHUP never arrives on Windows,
// which is harmless.
func hangups(ctx context.Context) <-chan struct{} {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	reload := make(chan struct{})
	go func() {
		defer signal.Stop(hup)
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				select {
				case reload <- struct{}{}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return reload
}
