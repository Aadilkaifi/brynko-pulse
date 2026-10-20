// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package heal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/fsutil"
)

// Approvals travel from `pulse fix` to the running service as small files in the
// data folder, like silences: the service never listens on a port. A request only
// names a monitor and a fix; what the fix does always comes from the configuration.

// Request actions.
const (
	ActionApprove = "approve"
	ActionDismiss = "dismiss"
)

const (
	requestsDir = "fix-requests"
	reqSuffix   = ".req.json"
	ansSuffix   = ".ans.json"
	// requestTTL drops a request the service finds too late, such as after a restart.
	requestTTL = time.Minute
)

// ErrNoService means no running pulse answered the request.
var ErrNoService = errors.New("pulse did not answer: the service is not running, or is older than this command")

// Request asks the running service to approve or dismiss a waiting fix.
type Request struct {
	Monitor string    `json:"monitor"`
	FixID   string    `json:"fix_id,omitempty"` // empty: whatever fix is waiting now
	Action  string    `json:"action"`
	At      time.Time `json:"at"`
}

// Answer is the service's reply.
type Answer struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	FixID string `json:"fix_id,omitempty"`
	What  string `json:"what,omitempty"` // the fix in words
}

// RequestsDir is where requests wait for the service.
func RequestsDir(dataDir string) string { return filepath.Join(dataDir, requestsDir) }

// PrepareRequests creates the requests folder. The service does it, so it owns the
// folder and can remove the requests it has handled.
func PrepareRequests(dataDir string) error {
	return os.MkdirAll(RequestsDir(dataDir), 0o750)
}

// Submit hands req to the running service and waits up to wait for its answer.
func Submit(ctx context.Context, dataDir string, req Request, wait time.Duration) (Answer, error) {
	dir := RequestsDir(dataDir)
	if _, err := os.Stat(dir); err != nil {
		return Answer{}, ErrNoService
	}
	data, err := json.Marshal(req)
	if err != nil {
		return Answer{}, err
	}
	name := randomID() + randomID()
	reqPath, ansPath := filepath.Join(dir, name+reqSuffix), filepath.Join(dir, name+ansSuffix)
	if err := fsutil.WriteAtomic(reqPath, data, 0o640); err != nil {
		return Answer{}, fmt.Errorf("hand the request to pulse: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if b, err := os.ReadFile(ansPath); err == nil {
			_ = os.Remove(ansPath)
			var a Answer
			if err := json.Unmarshal(b, &a); err != nil {
				return Answer{}, fmt.Errorf("read pulse's answer: %w", err)
			}
			return a, nil
		}
		select {
		case <-ctx.Done():
			_ = os.Remove(reqPath)
			return Answer{}, ErrNoService
		case <-tick.C:
		}
	}
}

// Serve answers every waiting request with handle. The service calls it every
// couple of seconds; an empty folder costs one directory read.
func Serve(dataDir string, now time.Time, handle func(Request) Answer) error {
	dir := RequestsDir(dataDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return PrepareRequests(dataDir)
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(dir, name)
		if strings.HasSuffix(name, ansSuffix) {
			// An answer nobody picked up, such as after the command was stopped.
			if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > requestTTL {
				_ = os.Remove(path)
			}
			continue
		}
		if !strings.HasSuffix(name, reqSuffix) {
			continue
		}
		b, err := os.ReadFile(path)
		_ = os.Remove(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var req Request
		var ans Answer
		switch err := json.Unmarshal(b, &req); {
		case err != nil:
			ans = Answer{Error: "the request could not be read"}
		case now.Sub(req.At) > requestTTL:
			ans = Answer{Error: "the request came too late; try again"}
		case req.Action != ActionApprove && req.Action != ActionDismiss:
			ans = Answer{Error: fmt.Sprintf("unknown action %q", req.Action)}
		default:
			ans = handle(req)
		}
		out, _ := json.Marshal(ans)
		ansPath := filepath.Join(dir, strings.TrimSuffix(name, reqSuffix)+ansSuffix)
		if err := fsutil.WriteAtomic(ansPath, out, 0o640); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
