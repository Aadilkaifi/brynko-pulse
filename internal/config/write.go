// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"fmt"
	"io/fs"
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
	Delete     bool // remove the file; only monitor files can be removed
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
		if c.Delete {
			if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return res, err
			}
			continue
		}
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
	o, err := overlayFor(dir, changes)
	if err != nil {
		return Result{Dir: dir, Errors: []Issue{}, Warnings: []Issue{}}, err
	}
	res, _, _ := validate(dir, o)
	if !res.OK() {
		return res, ErrInvalid
	}
	return res, nil
}

// overlayFor is the configuration folder as it would be after changes.
func overlayFor(dir string, changes []Change) (overlay, error) {
	o := overlay{base: dirSource(dir), changed: map[string][]byte{}, deleted: map[string]bool{}}
	for _, c := range changes {
		if err := checkChangePath(c.Path); err != nil {
			return o, err
		}
		if c.Delete {
			if path.Dir(c.Path) != MonitorsDir {
				return o, fmt.Errorf("%s cannot be deleted; edit it instead", c.Path)
			}
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(c.Path))); err != nil {
				return o, fmt.Errorf("%s does not exist", c.Path)
			}
			o.deleted[c.Path] = true
			continue
		}
		if c.CreateOnly {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(c.Path))); err == nil {
				return o, fmt.Errorf("%s already exists; nothing was written", c.Path)
			}
		}
		o.changed[c.Path] = c.Data
	}
	return o, nil
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
