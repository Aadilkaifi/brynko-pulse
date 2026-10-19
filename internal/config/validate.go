// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
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
	r, _, _ := validate(dir, dirSource(dir))
	return r
}

// Load validates dir and returns the merged configuration. cfg is nil when the
// result has errors.
func Load(dir string) (*Config, Result) {
	return loadFrom(dir, dirSource(dir))
}

// Preview returns the configuration as it would be after changes, without
// writing anything. cfg is nil when the result has errors.
func Preview(dir string, changes []Change) (*Config, Result) {
	o, err := overlayFor(dir, changes)
	if err != nil {
		return nil, Result{Dir: dir, Errors: []Issue{{Message: err.Error()}}, Warnings: []Issue{}}
	}
	return loadFrom(dir, o)
}

func loadFrom(dir string, src source) (*Config, Result) {
	r, docs, secrets := validate(dir, src)
	if !r.OK() {
		return nil, r
	}
	cfg, err := merge(docs)
	if err != nil {
		r.Errors = append(r.Errors, Issue{Message: "cannot read the configuration: " + err.Error()})
		return nil, r
	}
	cfg.Secrets = secrets
	return cfg, r
}

func merge(docs []document) (*Config, error) {
	cfg := &Config{Channels: map[string]Channel{}}
	for _, d := range docs {
		raw, err := json.Marshal(d.value)
		if err != nil {
			return nil, err
		}
		switch d.kind {
		case kindPulse:
			if err := json.Unmarshal(raw, cfg); err != nil {
				return nil, fmt.Errorf("%s: %w", d.name, err)
			}
		case kindChannels:
			var f struct{ Channels map[string]Channel }
			if err := json.Unmarshal(raw, &f); err != nil {
				return nil, fmt.Errorf("%s: %w", d.name, err)
			}
			for id, ch := range f.Channels {
				cfg.Channels[id] = ch
			}
		case kindMonitors:
			var f struct{ Monitors []Monitor }
			if err := json.Unmarshal(raw, &f); err != nil {
				return nil, fmt.Errorf("%s: %w", d.name, err)
			}
			for _, m := range f.Monitors {
				m.SourceFile = d.name
				cfg.Monitors = append(cfg.Monitors, m)
			}
		}
	}
	if cfg.Limits.MaxAlertsPerMinute == 0 {
		cfg.Limits.MaxAlertsPerMinute = 10
	}
	if cfg.Limits.CommandOutputBytes == 0 {
		cfg.Limits.CommandOutputBytes = 64 << 10
	}
	if cfg.Limits.MaxHealsPerHour == 0 {
		cfg.Limits.MaxHealsPerHour = 10
	}
	return cfg, nil
}

func validate(dir string, src source) (Result, []document, Secrets) {
	r := Result{Dir: dir, Errors: []Issue{}, Warnings: []Issue{}}

	docs, issues := load(src)
	r.Errors = append(r.Errors, issues...)
	secrets, issues := readSecrets(src)
	r.Errors = append(r.Errors, issues...)

	s, err := compiled()
	if err != nil {
		r.Errors = append(r.Errors, Issue{Message: "the built-in schema is broken: " + err.Error()})
		return r, nil, nil
	}
	for _, d := range docs {
		if err := s.byKind[d.kind].Validate(d.value); err != nil {
			r.Errors = append(r.Errors, schemaIssues(d, err, s)...)
		}
	}
	if r.OK() {
		crossCheck(docs, secrets, &r)
	}
	sortIssues(r.Errors)
	sortIssues(r.Warnings)
	return r, docs, secrets
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

// reservedID names every monitor at once, as in "pulse silence all".
const reservedID = "all"

var envRef = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// checkWatchdog checks that the watchdog's channels exist and that the relay has
// someone to email: it can only email, since other channels' addresses hold secrets.
func checkWatchdog(d document, w map[string]any, defaultNotify []any, channels map[string]bool, emailTo map[string]int, r *Result) {
	ids, own := w["notify"].([]any)
	if !own {
		ids = defaultNotify
	}
	addresses := 0
	var skipped []string
	for i, v := range ids {
		id := v.(string)
		switch {
		case !channels[id] && own:
			r.Errors = append(r.Errors, issueAt(d, []string{"watchdog", "notify", fmt.Sprint(i)},
				fmt.Sprintf("channel %q is not defined in %s", id, ChannelsFile)))
		case !channels[id]:
			// reported under defaults.notify
		case emailTo[id] == 0:
			skipped = append(skipped, id)
		default:
			addresses += emailTo[id]
		}
	}
	if len(skipped) > 0 {
		r.Warnings = append(r.Warnings, issueAt(d, []string{"watchdog"}, fmt.Sprintf(
			"the watchdog emails only: %s will not be told when this server goes silent", strings.Join(skipped, ", "))))
	}
	if addresses == 0 {
		r.Warnings = append(r.Warnings, issueAt(d, []string{"watchdog"},
			"the watchdog names no email address, so the brynko relay emails the account that turned the alert on; name an email channel in watchdog.notify or defaults.notify to choose who it alerts"))
	}
}

// crossCheck finds problems that span files. It runs only when every file
// matches the schema, so type assertions below are safe.
func crossCheck(docs []document, secrets Secrets, r *Result) {
	channels := map[string]bool{}
	emailTo := map[string]int{} // email channel -> how many addresses
	var defaultNotify []any
	var watchdog map[string]any
	var pulseDoc document
	for _, d := range docs {
		m := d.value.(map[string]any)
		switch d.kind {
		case kindChannels:
			for id, ch := range m["channels"].(map[string]any) {
				channels[id] = true
				if c, _ := ch.(map[string]any); c["type"] == "smtp" {
					to, _ := c["to"].([]any)
					emailTo[id] = len(to)
				}
			}
		case kindPulse:
			pulseDoc = d
			if def, ok := m["defaults"].(map[string]any); ok {
				defaultNotify, _ = def["notify"].([]any)
			}
			watchdog, _ = m["watchdog"].(map[string]any)
		}
	}

	for i, id := range defaultNotify {
		if !channels[id.(string)] {
			r.Errors = append(r.Errors, issueAt(pulseDoc, []string{"defaults", "notify", fmt.Sprint(i)},
				fmt.Sprintf("channel %q is not defined in %s", id, ChannelsFile)))
		}
	}

	if watchdog != nil {
		checkWatchdog(pulseDoc, watchdog, defaultNotify, channels, emailTo, r)
	}

	monitorIDs := map[string]string{}
	monitors := 0
	type healDep struct {
		doc          document
		path         []string
		monitor, dep string
	}
	var healDeps []healDep
	var silent []Issue
	for _, d := range docs {
		if d.kind != kindMonitors {
			continue
		}
		for i, item := range d.value.(map[string]any)["monitors"].([]any) {
			monitors++
			mon := item.(map[string]any)
			id := mon["id"].(string)
			base := []string{"monitors", fmt.Sprint(i)}
			if id == reservedID {
				r.Errors = append(r.Errors, issueAt(d, append(base, "id"),
					`monitor id "all" is reserved: "pulse silence all" means every monitor. Pick another id`))
			}
			if first, dup := monitorIDs[id]; dup {
				r.Errors = append(r.Errors, issueAt(d, append(base, "id"),
					fmt.Sprintf("monitor id %q is already used in %s", id, first)))
			} else {
				monitorIDs[id] = d.name
			}
			if s, set := mon["json_expect"].(string); set {
				if _, path := mon["json_path"]; !path {
					r.Errors = append(r.Errors, issueAt(d, append(base, "json_expect"),
						"json_expect compares the JSON field that json_path names; set json_path too"))
				} else if _, err := rule.ParseExpect(s); err != nil {
					r.Errors = append(r.Errors, issueAt(d, append(base, "json_expect"), "json_expect: "+err.Error()))
				}
			}
			if heal, ok := mon["heal"].(map[string]any); ok {
				if t, _ := mon["type"].(string); len(HealSteps(t)) == 0 {
					r.Errors = append(r.Errors, issueAt(d, append(base, "heal"), fmt.Sprintf(
						"%s monitors cannot have heal: restarting something or running a program does not fix what they watch. Remove heal", t)))
				}
				steps, _ := heal["steps"].([]any)
				for j, step := range steps {
					if n := len(step.(map[string]any)); n != 1 {
						r.Errors = append(r.Errors, issueAt(d, append(base, "heal", "steps", fmt.Sprint(j)),
							"heal: each step does one thing: restart_service, restart_container or run. Put them in separate steps"))
					}
				}
				deps, _ := heal["only_if_ok"].([]any)
				for j, dep := range deps {
					healDeps = append(healDeps, healDep{doc: d, path: append(base, "heal", "only_if_ok", fmt.Sprint(j)),
						monitor: id, dep: dep.(string)})
				}
			}
			notify, has := mon["notify"].([]any)
			for j, ch := range notify {
				if !channels[ch.(string)] {
					r.Errors = append(r.Errors, issueAt(d, append(base, "notify", fmt.Sprint(j)),
						fmt.Sprintf("channel %q is not defined in %s", ch, ChannelsFile)))
				}
			}
			if !has && len(defaultNotify) == 0 && len(channels) > 0 {
				silent = append(silent, issueAt(d, base,
					fmt.Sprintf("monitor %q has no channels, so its alerts go nowhere. Add notify here or under defaults in %s.", id, PulseFile)))
			}
		}
	}
	for _, h := range healDeps {
		switch _, known := monitorIDs[h.dep]; {
		case h.dep == h.monitor:
			r.Errors = append(r.Errors, issueAt(h.doc, h.path, "heal: only_if_ok lists the monitor itself; list the monitors it depends on"))
		case !known:
			r.Errors = append(r.Errors, issueAt(h.doc, h.path, fmt.Sprintf("heal: only_if_ok names %q, which is not a monitor", h.dep)))
		}
	}
	// One warning, not one per monitor, when the fix is a single line in pulse.yaml.
	if len(silent) > 3 {
		silent = []Issue{{File: PulseFile, Message: fmt.Sprintf(
			"%d monitors have no channels, so their alerts go nowhere. Add notify under defaults in %s, for example notify: [%s].",
			len(silent), PulseFile, firstKey(channels))}}
	}
	r.Warnings = append(r.Warnings, silent...)
	if len(channels) == 0 && monitors > 0 {
		r.Warnings = append(r.Warnings, Issue{File: ChannelsFile,
			Message: "no alert channels are set up yet, so alerts go nowhere. Add one in " + ChannelsFile + "."})
	}
	if monitors == 0 {
		r.Warnings = append(r.Warnings, Issue{File: MonitorsDir, Message: "no monitors are configured yet"})
	}

	checkMaintenance(pulseDoc, monitorIDs, r)

	for _, d := range docs {
		walkStrings(d.value, nil, func(path []string, s string) {
			m := envRef.FindStringSubmatch(s)
			if m == nil {
				return
			}
			if _, set := secrets.Lookup(m[1]); !set {
				r.Warnings = append(r.Warnings, issueAt(d, path,
					fmt.Sprintf("%s is not set. Add it to %s in the config folder.", m[1], SecretsFile)))
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

// checkMaintenance finds what the schema cannot: unknown time zones, empty
// windows, repeated names and monitors that do not exist.
func checkMaintenance(d document, monitorIDs map[string]string, r *Result) {
	if d.value == nil {
		return
	}
	list, _ := d.value.(map[string]any)["maintenance"].([]any)
	names := map[string]bool{}
	for i, item := range list {
		w := item.(map[string]any)
		base := []string{"maintenance", fmt.Sprint(i)}
		name, _ := w["name"].(string)
		if names[name] {
			r.Errors = append(r.Errors, issueAt(d, append(base, "name"), fmt.Sprintf("maintenance window %q is defined twice", name)))
		}
		names[name] = true
		if w["from"] == w["to"] {
			r.Errors = append(r.Errors, issueAt(d, append(base, "to"), "from and to are the same time, so the window would never open"))
		}
		if tz, ok := w["timezone"].(string); ok {
			if _, err := time.LoadLocation(tz); err != nil {
				r.Errors = append(r.Errors, issueAt(d, append(base, "timezone"),
					fmt.Sprintf("%q is not a known time zone; use a name like Asia/Kolkata or UTC", tz)))
			}
		}
		ids, _ := w["monitors"].([]any)
		for j, id := range ids {
			if _, ok := monitorIDs[id.(string)]; !ok {
				r.Errors = append(r.Errors, issueAt(d, append(base, "monitors", fmt.Sprint(j)),
					fmt.Sprintf("there is no monitor with id %q", id)))
			}
		}
	}
}

func firstKey(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}
