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
	"regexp"
	"sort"
	"strings"

	"github.com/Aadilkaifi/brynko-pulse/examples"
)

// ErrAlreadySetUp means Init found existing configuration and changed nothing.
var ErrAlreadySetUp = errors.New("configuration already exists")

// InitOptions controls Init.
type InitOptions struct {
	Dir        string
	ServerName string // sanitised; empty becomes "server"
	RootDisk   string // path the starter disk monitor watches
}

// InitResult lists what Init wrote.
type InitResult struct {
	Dir     string   `json:"config_dir"`
	Created []string `json:"created"`
}

// ExistsError lists the files that stopped Init.
type ExistsError struct{ Files []string }

func (e *ExistsError) Error() string {
	return "found " + strings.Join(e.Files, ", ")
}

func (e *ExistsError) Unwrap() error { return ErrAlreadySetUp }

// Init writes the starter configuration into opts.Dir. It never overwrites:
// if any starter file already exists, nothing is written.
func Init(opts InitOptions) (InitResult, error) {
	res := InitResult{Dir: opts.Dir, Created: []string{}}
	files, err := starterFiles(opts)
	if err != nil {
		return res, err
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var existing []string
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(opts.Dir, filepath.FromSlash(name))); err == nil {
			existing = append(existing, name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
	}
	if len(existing) > 0 {
		return res, &ExistsError{Files: existing}
	}

	for _, name := range names {
		target := filepath.Join(opts.Dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
			return res, err
		}
		if err := writeFileAtomic(target, files[name]); err != nil {
			return res, err
		}
		res.Created = append(res.Created, name)
	}
	return res, nil
}

func starterFiles(opts InitOptions) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(examples.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (path.Ext(p) != ".yaml" && p != SecretsFile) {
			return err
		}
		data, err := examples.FS.ReadFile(p)
		if err != nil {
			return err
		}
		out[p] = data
		return nil
	})
	if err != nil {
		return nil, err
	}

	name := SanitizeServerName(opts.ServerName)
	out[PulseFile] = []byte(strings.Replace(string(out[PulseFile]),
		"server_name: web-1", "server_name: "+name, 1))

	if opts.RootDisk != "" && opts.RootDisk != "/" {
		sys := MonitorsDir + "/system.yaml"
		out[sys] = []byte(strings.Replace(string(out[sys]),
			"    path: /\n", fmt.Sprintf("    path: '%s'\n", opts.RootDisk), 1))
	}
	return out, nil
}

var invalidNameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// SanitizeServerName turns a host name into a valid server_name.
func SanitizeServerName(s string) string {
	s = strings.Trim(invalidNameChars.ReplaceAllString(s, "-"), "-._")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-._")
	}
	if s == "" {
		return "server"
	}
	return s
}
