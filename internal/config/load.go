// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package config loads, validates and (later) applies pulse configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Names inside the config directory.
const (
	PulseFile    = "pulse.yaml"
	ChannelsFile = "channels.yaml"
	MonitorsDir  = "monitors"
)

const maxFileSize = 1 << 20

type fileKind string

const (
	kindPulse    fileKind = "pulseFile"
	kindChannels fileKind = "channelsFile"
	kindMonitors fileKind = "monitorsFile"
)

type document struct {
	name  string // relative to the config dir, forward slashes
	kind  fileKind
	node  *yaml.Node
	value any
}

type lineError struct {
	line int
	msg  string
}

func (e *lineError) Error() string { return e.msg }

func load(src source) ([]document, []Issue) {
	var docs []document
	var issues []Issue

	add := func(rel string, kind fileKind, required bool) {
		d, err := readDocument(src, rel, kind)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if required {
				issues = append(issues, Issue{File: rel, Message: rel + " is missing. Every configuration needs one; see examples/pulse.yaml."})
			}
		case err != nil:
			issues = append(issues, issueFromError(rel, err))
		default:
			docs = append(docs, d)
		}
	}

	add(PulseFile, kindPulse, true)
	add(ChannelsFile, kindChannels, false)

	names, err := src.files(MonitorsDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		issues = append(issues, Issue{File: MonitorsDir, Message: "cannot read the monitors folder: " + err.Error()})
	}
	for _, name := range names {
		if !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		add(MonitorsDir+"/"+name, kindMonitors, true)
	}
	return docs, issues
}

func readDocument(src source, rel string, kind fileKind) (document, error) {
	f, err := src.open(rel)
	if err != nil {
		return document{}, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return document{}, err
	}
	if len(data) > maxFileSize {
		return document{}, errors.New("file is larger than 1 MiB")
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return document{}, errors.New("file is empty")
		}
		return document{}, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return document{}, errors.New("only one YAML document per file is allowed (remove the extra ---)")
	}

	top := &root
	if top.Kind == yaml.DocumentNode && len(top.Content) == 1 {
		top = top.Content[0]
	}
	v, err := toValue(top)
	if err != nil {
		return document{}, err
	}
	return document{name: rel, kind: kind, node: top, value: v}, nil
}

// toValue converts YAML into plain JSON-like values. Aliases and merge keys are
// rejected: they make files harder to read and can expand without bound.
func toValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode {
				return nil, &lineError{k.Line, "keys must be plain text"}
			}
			if k.Tag == "!!merge" {
				return nil, &lineError{k.Line, "YAML merge keys (<<) are not supported"}
			}
			if _, dup := m[k.Value]; dup {
				return nil, &lineError{k.Line, fmt.Sprintf("%q is defined twice", k.Value)}
			}
			v, err := toValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := toValue(c)
			if err != nil {
				return nil, err
			}
			s = append(s, v)
		}
		return s, nil
	case yaml.ScalarNode:
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, &lineError{n.Line, err.Error()}
		}
		switch v.(type) {
		case time.Time:
			return n.Value, nil
		case []byte:
			return nil, &lineError{n.Line, "binary values are not supported"}
		}
		return v, nil
	case yaml.AliasNode:
		return nil, &lineError{n.Line, "YAML anchors and aliases are not supported"}
	default:
		return nil, &lineError{n.Line, "unsupported YAML content"}
	}
}

// lineAt returns the line of the deepest node found along path.
func lineAt(n *yaml.Node, path []string) int {
	line := n.Line
	for _, seg := range path {
		var next *yaml.Node
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == seg {
					next = n.Content[i+1]
					line = n.Content[i].Line
					break
				}
			}
		case yaml.SequenceNode:
			if idx, err := strconv.Atoi(seg); err == nil && idx >= 0 && idx < len(n.Content) {
				next = n.Content[idx]
				line = next.Line
			}
		}
		if next == nil {
			return line
		}
		n = next
	}
	return line
}

var yamlLine = regexp.MustCompile(`line (\d+)`)

func issueFromError(rel string, err error) Issue {
	var le *lineError
	if errors.As(err, &le) {
		return Issue{File: rel, Line: le.line, Message: le.msg}
	}
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	issue := Issue{File: rel, Message: msg}
	if m := yamlLine.FindStringSubmatch(msg); m != nil {
		issue.Line, _ = strconv.Atoi(m[1])
		issue.Message = strings.TrimSpace(yamlLine.ReplaceAllString(msg, ""))
		issue.Message = strings.TrimPrefix(issue.Message, ": ")
	}
	return issue
}
