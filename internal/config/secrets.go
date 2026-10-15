// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SecretsFile holds NAME=value lines that ${NAME} references resolve to.
const SecretsFile = "secrets.env"

const maxSecretsSize = 64 << 10

var secretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Secrets are the values from secrets.env. A variable set in the environment
// takes precedence, so a one-off override never needs the file edited.
type Secrets map[string]string

// Lookup returns the value for name and whether it is set anywhere.
func (s Secrets) Lookup(name string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}
	v, ok := s[name]
	return v, ok
}

// Resolve turns a ${NAME} reference into its value. Plain values are returned unchanged.
func (s Secrets) Resolve(ref string) string {
	if m := envRef.FindStringSubmatch(ref); m != nil {
		v, _ := s.Lookup(m[1])
		return v
	}
	return ref
}

// SecretsPath is where secrets.env lives inside dir.
func SecretsPath(dir string) string { return filepath.Join(dir, SecretsFile) }

// readSecrets loads secrets.env from dir. A missing file is not an error.
// Issues never contain a secret value.
func readSecrets(src source) (Secrets, []Issue) {
	f, err := src.open(SecretsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return Secrets{}, nil
	}
	if err != nil {
		return Secrets{}, []Issue{{File: SecretsFile, Message: "cannot read " + SecretsFile + ": " + err.Error()}}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSecretsSize+1))
	if err != nil {
		return Secrets{}, []Issue{{File: SecretsFile, Message: "cannot read " + SecretsFile + ": " + err.Error()}}
	}
	if len(data) > maxSecretsSize {
		return Secrets{}, []Issue{{File: SecretsFile, Message: fmt.Sprintf("%s is larger than %d KiB", SecretsFile, maxSecretsSize>>10)}}
	}
	return parseSecrets(data)
}

func parseSecrets(data []byte) (Secrets, []Issue) {
	out := Secrets{}
	var issues []Issue
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), maxSecretsSize)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		bad := func(msg string) { issues = append(issues, Issue{File: SecretsFile, Line: n, Message: msg}) }
		switch {
		case !ok:
			bad("expected NAME=value")
			continue
		case !secretName.MatchString(name):
			bad(fmt.Sprintf("%q is not a valid name: use letters, digits and _, not starting with a digit", name))
			continue
		}
		if _, dup := out[name]; dup {
			bad(name + " is set twice")
			continue
		}
		out[name] = unquote(strings.TrimSpace(value))
	}
	if err := sc.Err(); err != nil {
		issues = append(issues, Issue{File: SecretsFile, Message: "cannot read " + SecretsFile + ": " + err.Error()})
	}
	return out, issues
}

// unquote removes one pair of matching quotes, as people often write them.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}
