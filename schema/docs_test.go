// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package schema

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Generated docs are rebuilt with: go test ./cmd/pulse ./schema -run Docs -update
var update = flag.Bool("update", false, "rewrite the generated docs")

type node = map[string]any

func defs(t *testing.T) node {
	var doc node
	if err := json.Unmarshal(JSON, &doc); err != nil {
		t.Fatal(err)
	}
	return doc["$defs"].(node)
}

// typeOf describes a property's type in a few words.
func typeOf(p node) string {
	if ref, ok := p["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		switch name {
		case "duration":
			return "duration (30s, 5m, 2h, 1d)"
		case "envRef":
			return "secret `${NAME}`"
		case "condition":
			return "condition (`metric > value`)"
		case "severity":
			return "`info` \\| `warning` \\| `critical`"
		case "id":
			return "id"
		case "notifyList":
			return "list of channel names"
		case "emailList":
			return "list of email addresses"
		case "phoneList":
			return "list of phone numbers (+919876543210)"
		case "clock":
			return "time `HH:MM`"
		case "serverName":
			return "name (letters, digits, `.` `_` `-`)"
		case "configVersion":
			return "`1`"
		case "maintenanceList":
			return "list of [maintenance](#maintenance) windows"
		}
		return "[" + name + "](#" + strings.ToLower(name) + ")"
	}
	if enum, ok := p["enum"].([]any); ok {
		parts := make([]string, len(enum))
		for i, e := range enum {
			parts[i] = fmt.Sprintf("`%v`", e)
		}
		return strings.Join(parts, " \\| ")
	}
	if c, ok := p["const"]; ok {
		return fmt.Sprintf("`%v`", c)
	}
	if one, ok := p["oneOf"].([]any); ok {
		parts := []string{}
		for _, o := range one {
			parts = append(parts, typeOf(o.(node)))
		}
		return strings.Join(parts, " or ")
	}
	typ, _ := p["type"].(string)
	if typ == "array" {
		if items, ok := p["items"].(node); ok {
			return "list of " + typeOf(items)
		}
	}
	if typ == "" {
		return "object"
	}
	return typ
}

func table(b *bytes.Buffer, props node, required []any) {
	req := map[string]bool{}
	for _, r := range required {
		req[r.(string)] = true
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if req[keys[i]] != req[keys[j]] {
			return req[keys[i]]
		}
		return keys[i] < keys[j]
	})
	b.WriteString("| Field | Type | Required | Default | Description |\n|---|---|---|---|---|\n")
	for _, k := range keys {
		p := props[k].(node)
		def := ""
		if d, ok := p["default"]; ok {
			def = fmt.Sprintf("`%v`", d)
		}
		yes := ""
		if req[k] {
			yes = "yes"
		}
		desc, _ := p["description"].(string)
		fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s |\n", k, typeOf(p), yes, def, desc)
	}
	b.WriteString("\n")
}

func section(b *bytes.Buffer, d node, name, title string) {
	def := d[name].(node)
	fmt.Fprintf(b, "### %s\n\n", title)
	if desc, ok := def["description"].(string); ok && !strings.HasSuffix(desc, ".yaml") {
		b.WriteString(desc + "\n\n")
	}
	props, _ := def["properties"].(node)
	req, _ := def["required"].([]any)
	table(b, props, req)
}

// variants returns the type-specific properties from the if/then branches.
func variants(def node) map[string]node {
	out := map[string]node{}
	all, _ := def["allOf"].([]any)
	for _, a := range all {
		br := a.(node)
		cond := br["if"].(node)["properties"].(node)["type"].(node)
		then := br["then"].(node)
		names := []string{}
		if c, ok := cond["const"].(string); ok {
			names = append(names, c)
		}
		for _, e := range asList(cond["enum"]) {
			names = append(names, e.(string))
		}
		for _, n := range names {
			out[n] = then
		}
	}
	return out
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func renderConfigDocs(t *testing.T) []byte {
	d := defs(t)
	var b bytes.Buffer
	b.WriteString("<!-- Generated from schema/pulse.schema.json. Do not edit: run `go test ./cmd/pulse ./schema -run Docs -update`. -->\n\n")
	b.WriteString("# Configuration reference\n\n")
	b.WriteString("Every field pulse accepts, generated from the published JSON Schema (`pulse schema`). For a guided\n")
	b.WriteString("introduction see [configuration.md](configuration.md). Unknown fields are errors, so a typo is\n")
	b.WriteString("caught by `pulse validate` instead of being ignored.\n\n")

	b.WriteString("## pulse.yaml\n\n")
	section(&b, d, "pulseFile", "Top level")
	section(&b, d, "defaults", "defaults")
	section(&b, d, "flapping", "flapping")
	section(&b, d, "limits", "limits")
	section(&b, d, "api", "api")
	section(&b, d, "maintenance", "maintenance")

	b.WriteString("## channels.yaml\n\n`channels` maps a name you choose (an id) to a channel. Every channel has:\n\n")
	ch := d["channel"].(node)
	table(&b, ch["properties"].(node), asList(ch["required"]))
	cv := variants(ch)
	for _, typ := range asList(ch["properties"].(node)["type"].(node)["enum"]) {
		then := cv[typ.(string)]
		fmt.Fprintf(&b, "### Channel type `%s`\n\n", typ)
		if then == nil {
			b.WriteString("No extra fields.\n\n")
			continue
		}
		props, _ := then["properties"].(node)
		table(&b, props, asList(then["required"]))
		if any := asList(then["anyOf"]); len(any) > 0 {
			b.WriteString("At least one of the delivery lists above is required.\n\n")
		}
	}

	b.WriteString("## monitors/*.yaml\n\nEach file has a `monitors` list. Every monitor has:\n\n")
	m := d["monitor"].(node)
	table(&b, m["properties"].(node), asList(m["required"]))
	mv := variants(m)
	for _, typ := range asList(m["properties"].(node)["type"].(node)["enum"]) {
		then := mv[typ.(string)]
		fmt.Fprintf(&b, "### Monitor type `%s`\n\n", typ)
		if then == nil {
			b.WriteString("No extra fields.\n\n")
			continue
		}
		props, _ := then["properties"].(node)
		if len(props) == 0 {
			b.WriteString("No extra fields.\n\n")
		} else {
			table(&b, props, asList(then["required"]))
		}
		if req := asList(then["required"]); len(props) == 0 && len(req) > 0 {
			parts := []string{}
			for _, r := range req {
				parts = append(parts, "`"+r.(string)+"`")
			}
			fmt.Fprintf(&b, "Requires %s.\n\n", strings.Join(parts, ", "))
		}
		if one := asList(then["oneOf"]); len(one) > 0 {
			parts := []string{}
			for _, o := range one {
				for _, r := range asList(o.(node)["required"]) {
					parts = append(parts, "`"+r.(string)+"`")
				}
			}
			fmt.Fprintf(&b, "Exactly one of %s.\n\n", strings.Join(parts, ", "))
		}
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

func TestDocsConfigReference(t *testing.T) {
	want := append(renderConfigDocs(t), '\n')
	path := filepath.Join("..", "docs", "config-reference.md")
	if *update {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Fatalf("%s is out of date with the schema; regenerate it with: go test ./cmd/pulse ./schema -run Docs -update", path)
	}
}

// Hand-written guides must cover every type the schema allows, so a new
// monitor or channel type cannot ship undocumented.
func TestDocsCoverEveryType(t *testing.T) {
	d := defs(t)
	for file, def := range map[string]string{"monitors.md": "monitor", "channels.md": "channel"} {
		data, err := os.ReadFile(filepath.Join("..", "docs", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range asList(d[def].(node)["properties"].(node)["type"].(node)["enum"]) {
			if !strings.Contains(string(data), "## `"+typ.(string)+"`") {
				t.Errorf("docs/%s has no section \"## `%s`\"", file, typ)
			}
		}
	}
}

// Every relative link in the docs must point at a file that exists.
func TestDocsLinks(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "docs", "*.md"))
	files = append(files, filepath.Join("..", "README.md"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range linkRe.FindAllStringSubmatch(string(data), -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			target, _, _ = strings.Cut(target, "#")
			if _, err := os.Stat(filepath.Join(filepath.Dir(f), filepath.FromSlash(target))); err != nil {
				t.Errorf("%s links to %s, which does not exist", f, m[1])
			}
		}
	}
}

var linkRe = regexp.MustCompile(`\]\(([^)\s]+)\)`)
