// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func TestCommandLineQuotesSpaces(t *testing.T) {
	got := commandLine(`C:\Program Files\brynko-pulse\pulse.exe`, []string{"run", "--config", `C:\Pulse Config`})
	want := `"C:\Program Files\brynko-pulse\pulse.exe" run --config "C:\Pulse Config"`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestHandlerReloadsAndStops(t *testing.T) {
	reloads := make(chan struct{}, 1)
	h := &handler{log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		fn: func(ctx context.Context, reload <-chan struct{}, _ *slog.Logger) error {
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-reload:
					reloads <- struct{}{}
				}
			}
		}}
	req := make(chan svc.ChangeRequest)
	status := make(chan svc.Status, 10)
	done := make(chan uint32)
	go func() { _, code := h.Execute(nil, req, status); done <- code }()

	req <- svc.ChangeRequest{Cmd: svc.ParamChange}
	select {
	case <-reloads:
	case <-time.After(5 * time.Second):
		t.Fatal("paramchange did not reload")
	}
	req <- svc.ChangeRequest{Cmd: svc.Stop}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not end the service")
	}
}
