// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
	"github.com/Aadilkaifi/brynko-pulse/schema"
)

func schemaTypes(t *testing.T) []string {
	t.Helper()
	var doc struct {
		Defs struct {
			Monitor struct {
				Properties struct {
					Type struct {
						Enum []string `json:"enum"`
					} `json:"type"`
				} `json:"properties"`
			} `json:"monitor"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(schema.JSON, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Defs.Monitor.Properties.Type.Enum
}

func TestCatalogCoversEveryTypeOnce(t *testing.T) {
	var got []string
	for _, ti := range Catalog {
		if slices.Contains(got, ti.Type) {
			t.Errorf("%s is listed twice", ti.Type)
		}
		got = append(got, ti.Type)
	}
	want := schemaTypes(t)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("catalog types %v, schema types %v", got, want)
	}
}

func TestCatalogConditionsUseTheTypesOwnValues(t *testing.T) {
	kinds := []string{"number", "flag", "duration", "text", "any"}
	for _, ti := range Catalog {
		if ti.Label == "" || ti.Group == "" || ti.Summary == "" || len(ti.Values) == 0 {
			t.Errorf("%s: label, group, summary and values are required", ti.Type)
		}
		if (ti.Default == "") == (ti.Starter == "") {
			t.Errorf("%s: exactly one of default and starter", ti.Type)
		}
		names := map[string]bool{}
		for _, v := range ti.Values {
			names[v.Name] = true
			if !slices.Contains(kinds, v.Kind) || v.Label == "" {
				t.Errorf("%s.%s: kind %q, label %q", ti.Type, v.Name, v.Kind, v.Label)
			}
		}
		for _, cond := range []string{ti.Default, ti.Starter} {
			if cond == "" {
				continue
			}
			c, err := rule.Parse(cond)
			if err != nil {
				t.Errorf("%s: %q: %v", ti.Type, cond, err)
				continue
			}
			if !names[c.Metric] {
				t.Errorf("%s: %q uses %s, which the type does not produce", ti.Type, cond, c.Metric)
			}
		}
	}
}

func TestNewUsesTheCatalogDefault(t *testing.T) {
	for _, ti := range Catalog {
		if ti.Default == "" {
			continue
		}
		m := config.Monitor{ID: "x", Type: ti.Type}
		_, def, _ := New(m, Deps{})
		if def != ti.Default {
			t.Errorf("%s: New gives %q, catalog says %q", ti.Type, def, ti.Default)
		}
	}
}

// The public docs describe the same values the catalog publishes.
func TestDocsListEveryValue(t *testing.T) {
	raw, err := os.ReadFile("../../docs/monitors.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, ti := range Catalog {
		head := "## `" + ti.Type + "`"
		i := strings.Index(doc, head)
		if i < 0 {
			t.Errorf("docs/monitors.md has no section for %s", ti.Type)
			continue
		}
		section := doc[i+len(head):]
		if j := strings.Index(section, "\n## "); j >= 0 {
			section = section[:j]
		}
		for _, v := range ti.Values {
			if !strings.Contains(section, "`"+v.Name+"`") {
				t.Errorf("docs/monitors.md, %s: value %s is not listed", ti.Type, v.Name)
			}
		}
	}
}

func TestChoicesAndReasonsNameRealFields(t *testing.T) {
	for _, ti := range Catalog {
		if ti.Choice != nil {
			for _, o := range ti.Choice.Options {
				for _, f := range o.Fields {
					if !slices.Contains(ti.Main, f) || !strings.Contains(string(schema.JSON), `"`+f+`": {`) {
						t.Errorf("%s: choice %q uses %s, which is not a main field in the schema", ti.Type, o.Label, f)
					}
				}
			}
		}
		for _, r := range ti.Because {
			if v, ok := valueInfo(ti, r.Flag); !ok || v.Kind != "flag" {
				t.Errorf("%s: reason %s is not one of its flags", ti.Type, r.Flag)
			}
			if _, ok := valueInfo(ti, r.Value); r.Value != "" && !ok {
				t.Errorf("%s: reason value %s is not one of its values", ti.Type, r.Value)
			}
		}
	}
}
