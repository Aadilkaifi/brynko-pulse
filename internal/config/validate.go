// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/Aadilkaifi/brynko-pulse/schema"
)

// Issue is one problem found in the configuration.
type Issue struct {
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// Result is the outcome of Validate. Errors block startup; warnings do not.
type Result struct {
	Dir      string  `json:"config_dir"`
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
}

// OK reports whether the configuration has no errors.
func (r Result) OK() bool { return len(r.Errors) == 0 }

// Validate checks every file in dir against the schema and against each other.
func Validate(dir string) Result {
	r := Result{Dir: dir, Errors: []Issue{}, Warnings: []Issue{}}

	docs, issues := load(dir)
	r.Errors = append(r.Errors, issues...)

	s, err := compiled()
	if err != nil {
		r.Errors = append(r.Errors, Issue{Message: "the built-in schema is broken: " + err.Error()})
		return r
	}
	for _, d := range docs {
		if err := s.byKind[d.kind].Validate(d.value); err != nil {
			r.Errors = append(r.Errors, schemaIssues(d, err, s)...)
		}
	}
	if r.OK() {
		crossCheck(docs, &r)
	}
	sortIssues(r.Errors)
	sortIssues(r.Warnings)
	return r
}

type compiledSchema struct {
	byKind map[fileKind]*jsonschema.Schema
	doc    any
}

var (
	compileOnce sync.Once
	compiledVal *compiledSchema
	compileErr  error
)

func compiled() (*compiledSchema, error) {
	compileOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema.JSON))
		if err != nil {
			compileErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(schema.ID, doc); err != nil {
			compileErr = err
			return
		}
		cs := &compiledSchema{byKind: map[fileKind]*jsonschema.Schema{}, doc: doc}
		for _, k := range []fileKind{kindPulse, kindChannels, kindMonitors} {
			sch, err := c.Compile(schema.ID + "#/$defs/" + string(k))
			if err != nil {
				compileErr = err
				return
			}
			cs.byKind[k] = sch
		}
		compiledVal = cs
	})
	return compiledVal, compileErr
}

var printer = message.NewPrinter(language.English)

func schemaIssues(d document, err error, s *compiledSchema) []Issue {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []Issue{{File: d.name, Message: err.Error()}}
	}
	var leaves []*jsonschema.ValidationError
	var collect func(e *jsonschema.ValidationError)
	collect = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			leaves = append(leaves, e)
			return
		}
		for _, c := range e.Causes {
			collect(c)
		}
	}
	collect(ve)

	// When a type-specific branch fails, its fields also show up as "unevaluated".
	// Those are noise: report them only if nothing else is wrong in that object.
	realErrorIn := map[string]bool{}
	for _, e := range leaves {
		if _, isFalse := e.ErrorKind.(*kind.FalseSchema); isFalse {
			continue
		}
		for i := 0; i <= len(e.InstanceLocation); i++ {
			realErrorIn[pointer(e.InstanceLocation[:i])] = true
		}
	}

	var out []Issue
	seen := map[string]bool{}
	for _, e := range leaves {
		if _, isFalse := e.ErrorKind.(*kind.FalseSchema); isFalse {
			if n := len(e.InstanceLocation); n > 0 && realErrorIn[pointer(e.InstanceLocation[:n-1])] {
				continue
			}
		}
		loc := e.InstanceLocation
		// Point at the offending key itself, not the object that holds it.
		if k, ok := e.ErrorKind.(*kind.AdditionalProperties); ok && len(k.Properties) > 0 {
			loc = append(append([]string{}, loc...), k.Properties[0])
		}
		msg := describe(e, s)
		path := pointer(loc)
		if key := path + "\x00" + msg; !seen[key] {
			seen[key] = true
			out = append(out, Issue{File: d.name, Line: lineAt(d.node, loc), Path: path, Message: msg})
		}
	}
	return out
}

func describe(e *jsonschema.ValidationError, s *compiledSchema) string {
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		return "missing required field: " + strings.Join(k.Missing, ", ")
	case *kind.AdditionalProperties:
		return "unknown field: " + strings.Join(k.Properties, ", ") + " (check the spelling)"
	case *kind.FalseSchema:
		// unevaluatedProperties rejects a field that is valid only for another type.
		if n := len(e.InstanceLocation); n > 0 {
			return "unknown field: " + e.InstanceLocation[n-1] + " (check the spelling, or whether this type supports it)"
		}
	case *kind.Enum:
		want := make([]string, len(k.Want))
		for i, w := range k.Want {
			want[i] = fmt.Sprint(w)
		}
		return "must be one of: " + strings.Join(want, ", ")
	case *kind.Pattern:
		if d := descriptionAt(s.doc, e.SchemaURL); d != "" {
			return fmt.Sprintf("%q is not valid. %s", k.Got, d)
		}
	}
	return e.ErrorKind.LocalizedString(printer)
}

// descriptionAt finds the "description" of the schema at an absolute schema URL.
func descriptionAt(doc any, url string) string {
	_, frag, ok := strings.Cut(url, "#")
	if !ok {
		return ""
	}
	cur := doc
	for _, seg := range strings.Split(strings.TrimPrefix(frag, "/"), "/") {
		if seg == "" {
			continue
		}
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		switch v := cur.(type) {
		case map[string]any:
			cur = v[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return ""
			}
			cur = v[i]
		default:
			return ""
		}
	}
	if m, ok := cur.(map[string]any); ok {
		if d, ok := m["description"].(string); ok {
			return d
		}
	}
	return ""
}

func pointer(loc []string) string {
	if len(loc) == 0 {
		return ""
	}
	parts := make([]string, len(loc))
	for i, p := range loc {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~", "~0"), "/", "~1")
	}
	return "/" + strings.Join(parts, "/")
}

var envRef = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// crossCheck finds problems that span files. It runs only when every file
// matches the schema, so type assertions below are safe.
func crossCheck(docs []document, r *Result) {
	channels := map[string]bool{}
	var defaultNotify []any
	var pulseDoc document
	for _, d := range docs {
		m := d.value.(map[string]any)
		switch d.kind {
		case kindChannels:
			for id := range m["channels"].(map[string]any) {
				channels[id] = true
			}
		case kindPulse:
			pulseDoc = d
			if def, ok := m["defaults"].(map[string]any); ok {
				defaultNotify, _ = def["notify"].([]any)
			}
		}
	}

	for i, id := range defaultNotify {
		if !channels[id.(string)] {
			r.Errors = append(r.Errors, issueAt(pulseDoc, []string{"defaults", "notify", fmt.Sprint(i)},
				fmt.Sprintf("channel %q is not defined in %s", id, ChannelsFile)))
		}
	}

	monitorIDs := map[string]string{}
	monitors := 0
	for _, d := range docs {
		if d.kind != kindMonitors {
			continue
		}
		for i, item := range d.value.(map[string]any)["monitors"].([]any) {
			monitors++
			mon := item.(map[string]any)
			id := mon["id"].(string)
			base := []string{"monitors", fmt.Sprint(i)}
			if first, dup := monitorIDs[id]; dup {
				r.Errors = append(r.Errors, issueAt(d, append(base, "id"),
					fmt.Sprintf("monitor id %q is already used in %s", id, first)))
			} else {
				monitorIDs[id] = d.name
			}
			notify, has := mon["notify"].([]any)
			for j, ch := range notify {
				if !channels[ch.(string)] {
					r.Errors = append(r.Errors, issueAt(d, append(base, "notify", fmt.Sprint(j)),
						fmt.Sprintf("channel %q is not defined in %s", ch, ChannelsFile)))
				}
			}
			if !has && len(defaultNotify) == 0 && len(channels) > 0 {
				r.Warnings = append(r.Warnings, issueAt(d, base,
					fmt.Sprintf("monitor %q has no channels, so its alerts go nowhere. Add notify here or under defaults in %s.", id, PulseFile)))
			}
		}
	}
	if len(channels) == 0 && monitors > 0 {
		r.Warnings = append(r.Warnings, Issue{File: ChannelsFile,
			Message: "no alert channels are set up yet, so alerts go nowhere. Add one in " + ChannelsFile + "."})
	}
	if monitors == 0 {
		r.Warnings = append(r.Warnings, Issue{File: MonitorsDir, Message: "no monitors are configured yet"})
	}

	for _, d := range docs {
		walkStrings(d.value, nil, func(path []string, s string) {
			m := envRef.FindStringSubmatch(s)
			if m == nil {
				return
			}
			if _, set := os.LookupEnv(m[1]); !set {
				r.Warnings = append(r.Warnings, issueAt(d, path,
					fmt.Sprintf("%s is not set in this environment. Make sure the pulse service has it.", m[1])))
			}
		})
	}
}

func issueAt(d document, path []string, msg string) Issue {
	return Issue{File: d.name, Line: lineAt(d.node, path), Path: pointer(path), Message: msg}
}

func walkStrings(v any, path []string, fn func([]string, string)) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkStrings(t[k], append(append([]string{}, path...), k), fn)
		}
	case []any:
		for i, e := range t {
			walkStrings(e, append(append([]string{}, path...), fmt.Sprint(i)), fn)
		}
	case string:
		fn(path, t)
	}
}

func sortIssues(s []Issue) {
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].File != s[j].File {
			return s[i].File < s[j].File
		}
		return s[i].Line < s[j].Line
	})
}
