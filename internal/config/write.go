// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Aadilkaifi/brynko-pulse/internal/fsutil"
)

const (
	dirPerm  = 0o750
	filePerm = 0o640
)

func writeFileAtomic(path string, data []byte) error {
	return fsutil.WriteAtomic(path, data, filePerm)
}

// ErrInvalid means the proposed configuration has errors, so nothing was written.
var ErrInvalid = errors.New("the change would make the configuration invalid; nothing was written")

// Change is one file to create or replace inside the config folder.
type Change struct {
	Path       string // relative, forward slashes, for example monitors/web.yaml
	Data       []byte
	CreateOnly bool // fail instead of replacing an existing file
}

// Apply is the one way pulse changes its own configuration. It validates the
// whole configuration with the changes laid over it and writes only when that
// passes, each file atomically. Reloading the running service is the caller's job.
func Apply(dir string, changes []Change) (Result, error) {
	res, err := check(dir, changes)
	if err != nil {
		return res, err
	}
	for _, c := range changes {
		target := filepath.Join(dir, filepath.FromSlash(c.Path))
		if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
			return res, err
		}
		if err := writeFileAtomic(target, c.Data); err != nil {
			return res, err
		}
	}
	return res, nil
}

// Check validates the configuration as it would be after changes, without writing.
func Check(dir string, changes []Change) Result {
	res, err := check(dir, changes)
	if err != nil && len(res.Errors) == 0 {
		res.Errors = append(res.Errors, Issue{Message: err.Error()})
	}
	return res
}

func check(dir string, changes []Change) (Result, error) {
	empty := Result{Dir: dir, Errors: []Issue{}, Warnings: []Issue{}}
	o := overlay{base: dirSource(dir), changed: map[string][]byte{}}
	for _, c := range changes {
		if err := checkChangePath(c.Path); err != nil {
			return empty, err
		}
		if c.CreateOnly {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(c.Path))); err == nil {
				return empty, fmt.Errorf("%s already exists; nothing was written", c.Path)
			}
		}
		o.changed[c.Path] = c.Data
	}
	res, _, _ := validate(dir, o)
	if !res.OK() {
		return res, ErrInvalid
	}
	return res, nil
}

// checkChangePath allows only the files pulse reads, so a change can never
// write outside the config folder or create something pulse would ignore.
func checkChangePath(p string) error {
	clean := path.Clean(p)
	if clean != p || strings.Contains(p, `\`) || path.IsAbs(p) || strings.HasPrefix(p, "../") {
		return fmt.Errorf("invalid configuration path %q", p)
	}
	switch {
	case p == PulseFile, p == ChannelsFile:
		return nil
	case path.Dir(p) == MonitorsDir && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")):
		return nil
	}
	return fmt.Errorf("pulse does not manage %q", p)
}
