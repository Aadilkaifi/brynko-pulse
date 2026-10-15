// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/quickadd"
)

// Generated docs are rebuilt with: go test ./cmd/pulse ./schema -run Docs -update
var update = flag.Bool("update", false, "rewrite the generated docs")

// docsPlatform pins the paths shown in help, so the generated page is the same
// on every system that runs the tests.
type docsPlatform struct{ platform.Platform }

func (docsPlatform) ConfigDir() string { return "/etc/brynko-pulse" }
func (docsPlatform) DataDir() string   { return "/var/lib/brynko-pulse" }
func (docsPlatform) RootDisk() string  { return "/" }

func renderCLIDocs(t *testing.T) []byte {
	platformFor = func() platform.Platform { return docsPlatform{platform.Current()} }
	t.Cleanup(func() { platformFor = platform.Current })

	var b bytes.Buffer
	b.WriteString("<!-- Generated from the pulse command table. Do not edit: run `go test ./cmd/pulse -run Docs -update`. -->\n\n")
	b.WriteString("# Command reference\n\n")
	b.WriteString("Every command prints this help with `pulse <command> -h`. Paths below are the Linux defaults; on\n")
	b.WriteString("Windows they are `%ProgramData%\\brynko-pulse` and `%ProgramData%\\brynko-pulse\\data`.\n\n")
	b.WriteString("Every command that prints data accepts `--json`; that output is a versioned contract (`api_version`).\n\n")
	section := ""
	for _, c := range commands {
		if c.section != section {
			section = c.section
			fmt.Fprintf(&b, "## %s\n\n", section)
			for _, x := range commands {
				if x.section == section {
					fmt.Fprintf(&b, "- [`pulse %s`](#pulse-%s): %s\n", x.name, x.name, x.summary)
				}
			}
			b.WriteString("\n")
		}
		var out, errb bytes.Buffer
		run([]string{c.name, "-h"}, &out, &errb)
		help := strings.TrimSpace(errb.String() + out.String())
		fmt.Fprintf(&b, "### pulse %s\n\n```text\n%s\n```\n\n", c.name, help)
	}
	b.WriteString("## Exit codes\n\n| Code | Meaning |\n|---|---|\n| 0 | Success |\n| 1 | The command ran but failed, or the configuration is invalid |\n| 2 | Wrong usage: unknown command, flag or arguments |\n")
	return b.Bytes()
}

func TestDocsCLIReference(t *testing.T) {
	checkGenerated(t, filepath.Join("..", "..", "docs", "cli.md"), renderCLIDocs(t))
}

func checkGenerated(t *testing.T, path string, want []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s is missing; create it with: go test ./cmd/pulse ./schema -run Docs -update", path)
	}
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Fatalf("%s is out of date with the code; regenerate it with: go test ./cmd/pulse ./schema -run Docs -update", path)
	}
}

// The add help lists the monitor types by hand, so it must keep up with quickadd.
func TestAddHelpListsEveryType(t *testing.T) {
	for _, typ := range quickadd.Types {
		if !strings.Contains(addDetails, typ) {
			t.Errorf("pulse add -h does not mention %s", typ)
		}
	}
}
