// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
)

// source is where configuration files are read from: the config folder, or the
// folder with proposed changes laid over it, so changes are validated before
// anything is written.
type source interface {
	open(rel string) (io.ReadCloser, error)
	// files lists the regular files directly inside rel.
	files(rel string) ([]string, error)
}

type dirSource string

func (d dirSource) open(rel string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(string(d), filepath.FromSlash(rel)))
}

func (d dirSource) files(rel string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(string(d), filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

type overlay struct {
	base    source
	changed map[string][]byte // relative, forward slashes
}

func (o overlay) open(rel string) (io.ReadCloser, error) {
	if data, ok := o.changed[rel]; ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return o.base.open(rel)
}

func (o overlay) files(rel string) ([]string, error) {
	names, err := o.base.files(rel)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	added := false
	for p := range o.changed {
		if dir, name := path.Split(p); path.Clean(dir) == rel && !seen[name] {
			names, seen[name], added = append(names, name), true, true
		}
	}
	if err != nil && !added {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}
