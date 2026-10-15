// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
)

const configDetails = `  pulse config show            the whole configuration, as tools and forms read it
  pulse config get <file>      print one file, for example monitors/web.yaml
  pulse config set <file>      replace one file with what is read from standard input
  pulse config delete <file>   remove one monitor file

set and delete check the whole configuration first and change nothing if it
would be invalid. The running service picks up the change.

set --dry-run only checks: it reports what is wrong and, for a monitor file,
runs each monitor in it once and shows what it measures. Nothing is saved.`

const secretDetails = `  pulse secret set <NAME>      store the value read from standard input in secrets.env
  pulse secret delete <NAME>   remove it

The value is read from standard input so it never appears in the process list
or shell history. Refer to it in channels.yaml as ${NAME}.`

// maxInput bounds what set reads from standard input, like the config loader does.
const maxInput = 1 << 20

func cmdConfig(args []string, stdout, stderr io.Writer) int {
	p := platformFor()
	fs := newFlags("config", stderr)
	cfgDir := fs.String("config", p.ConfigDir(), "configuration directory")
	dataDir := fs.String("data", p.DataDir(), "where state and alert history are kept")
	dryRun := fs.Bool("dry-run", false, "set: check the file and test its monitors, without saving")
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(reorderFlags(args)) != nil {
		return exitUsage
	}
	a := fs.Args()
	switch {
	case len(a) == 2 && a[0] == "set" && *dryRun:
		data, err := readInput()
		if err != nil {
			return fail(stdout, stderr, *asJSON, "config", err)
		}
		return configDryRun(p, *cfgDir, *dataDir, a[1], data, *asJSON, stdout, stderr)
	case len(a) == 1 && a[0] == "show":
		return configShow(*cfgDir, *asJSON, stdout, stderr)
	case len(a) == 2 && a[0] == "get":
		return configGet(*cfgDir, a[1], *asJSON, stdout, stderr)
	case len(a) == 2 && (a[0] == "set" || a[0] == "delete"):
		data := []byte(nil)
		if a[0] == "set" {
			var err error
			if data, err = readInput(); err != nil {
				return fail(stdout, stderr, *asJSON, "config", err)
			}
		}
		change := config.Change{Path: a[1], Data: data, Delete: a[0] == "delete"}
		res, err := config.Apply(*cfgDir, []config.Change{change})
		return finishChange(p, "config", a[1], a[0], res, err, *asJSON, stdout, stderr)
	}
	fs.Usage()
	return exitUsage
}

func cmdSecret(args []string, stdout, stderr io.Writer) int {
	p := platformFor()
	fs := newFlags("secret", stderr)
	cfgDir := fs.String("config", p.ConfigDir(), "configuration directory")
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(reorderFlags(args)) != nil {
		return exitUsage
	}
	a := fs.Args()
	if len(a) != 2 || (a[0] != "set" && a[0] != "delete") {
		fs.Usage()
		return exitUsage
	}
	value := ""
	if a[0] == "set" {
		data, err := readInput()
		if err != nil {
			return fail(stdout, stderr, *asJSON, "secret", err)
		}
		if value = strings.TrimRight(string(data), "\r\n"); value == "" {
			return fail(stdout, stderr, *asJSON, "secret", errors.New("no value was given on standard input"))
		}
	}
	err := config.SetSecret(*cfgDir, a[1], value)
	res := config.Result{Dir: *cfgDir, Errors: []config.Issue{}, Warnings: []config.Issue{}}
	if err == nil {
		res = config.Validate(*cfgDir)
	}
	return finishChange(p, "secret", a[1], a[0], res, err, *asJSON, stdout, stderr)
}

func readInput() ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(bufio.NewReader(stdin), maxInput+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInput {
		return nil, fmt.Errorf("the input is larger than %d KiB", maxInput>>10)
	}
	return data, nil
}

// finishChange reloads the running service after a successful change and reports.
func finishChange(p platform.Platform, command, target, verb string, res config.Result, err error, asJSON bool, stdout, stderr io.Writer) int {
	saved, reloaded := err == nil, false
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		reloaded, err = p.ReloadService(ctx)
		cancel()
		if err != nil {
			err = fmt.Errorf("saved, but the running service could not be reloaded: %w", err)
		}
	}
	if asJSON {
		return finishJSON(stdout, stderr, struct {
			envelope
			Target   string         `json:"target"`
			Saved    bool           `json:"saved"`
			Reloaded bool           `json:"reloaded"`
			Errors   []config.Issue `json:"errors"`
			Warnings []config.Issue `json:"warnings"`
		}{newEnvelope(command, err), target, saved, reloaded, nonNil(res.Errors), nonNil(res.Warnings)}, err)
	}
	for _, is := range res.Errors {
		fmt.Fprintf(stdout, "error    %s\n", formatIssue(is))
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	done := map[string]string{"set": "Saved", "delete": "Deleted"}[verb]
	if reloaded {
		fmt.Fprintf(stdout, "%s %s. The running service picked it up.\n", done, target)
	} else {
		fmt.Fprintf(stdout, "%s %s. It applies when pulse next starts.\n", done, target)
	}
	return exitOK
}

// configDryRun checks a file as if it were saved and tests the monitors in it.
func configDryRun(p platform.Platform, cfgDir, dataDir, file string, data []byte, asJSON bool, stdout, stderr io.Writer) int {
	cfg, res := config.Preview(cfgDir, []config.Change{{Path: file, Data: data}})
	tests := []monitorTest{}
	var err error
	if cfg == nil {
		err = config.ErrInvalid
	} else if path.Dir(file) == config.MonitorsDir {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		for _, m := range cfg.Monitors {
			if m.SourceFile != file {
				continue
			}
			t, terr := checkMonitor(ctx, cfg, m, dataDir, p)
			if terr != nil {
				t = monitorTest{Monitor: m.ID, Values: rule.Values{}, WouldAlert: true, Detail: "cannot test it: " + terr.Error()}
			}
			tests = append(tests, t)
		}
		cancel()
	}
	if asJSON {
		return finishJSON(stdout, stderr, struct {
			envelope
			Target   string         `json:"target"`
			Saved    bool           `json:"saved"`
			DryRun   bool           `json:"dry_run"`
			Errors   []config.Issue `json:"errors"`
			Warnings []config.Issue `json:"warnings"`
			Tests    []monitorTest  `json:"tests"`
		}{newEnvelope("config", err), file, false, true, nonNil(res.Errors), nonNil(res.Warnings), tests}, err)
	}
	for _, is := range res.Errors {
		fmt.Fprintf(stdout, "error    %s\n", formatIssue(is))
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	for _, t := range tests {
		fmt.Fprintf(stdout, "%s\n", t.Monitor)
		printMonitorTest(stdout, t)
	}
	fmt.Fprintln(stdout, "The configuration would be valid. Nothing was saved (--dry-run).")
	return exitOK
}

func nonNil(l []config.Issue) []config.Issue {
	if l == nil {
		return []config.Issue{}
	}
	return l
}

// monitorView is one monitor with the file it lives in, so a form can save it back.
type monitorView struct {
	File string `json:"file"`
	config.Monitor
}

func configShow(dir string, asJSON bool, stdout, stderr io.Writer) int {
	cfg, res := config.Load(dir)
	secrets, serr := config.SecretNames(dir)
	if serr != nil {
		secrets = []string{}
	}
	sort.Strings(secrets)
	files := configFiles(dir)
	out := struct {
		envelope
		Dir         string                    `json:"config_dir"`
		Valid       bool                      `json:"valid"`
		Errors      []config.Issue            `json:"errors"`
		Warnings    []config.Issue            `json:"warnings"`
		Files       []string                  `json:"files"`
		Secrets     []string                  `json:"secrets"`
		ServerName  string                    `json:"server_name,omitempty"`
		Defaults    *config.Defaults          `json:"defaults,omitempty"`
		Limits      *config.Limits            `json:"limits,omitempty"`
		Maintenance []config.Maintenance      `json:"maintenance"`
		Channels    map[string]config.Channel `json:"channels"`
		Monitors    []monitorView             `json:"monitors"`
	}{envelope: newEnvelope("config", nil), Dir: dir, Valid: res.OK(), Errors: nonNil(res.Errors),
		Warnings: nonNil(res.Warnings), Files: files, Secrets: secrets,
		Maintenance: []config.Maintenance{}, Channels: map[string]config.Channel{}, Monitors: []monitorView{}}
	if cfg != nil {
		out.ServerName, out.Defaults, out.Limits = cfg.ServerName, &cfg.Defaults, &cfg.Limits
		if cfg.Maintenance != nil {
			out.Maintenance = cfg.Maintenance
		}
		out.Channels = cfg.Channels
		for _, m := range cfg.Monitors {
			out.Monitors = append(out.Monitors, monitorView{File: m.SourceFile, Monitor: m})
		}
	}
	if asJSON {
		return writeJSON(stdout, stderr, out)
	}
	fmt.Fprintf(stdout, "%s: %d channel(s), %d monitor(s), %d secret(s)\n", dir, len(out.Channels), len(out.Monitors), len(secrets))
	for _, f := range files {
		fmt.Fprintln(stdout, "  "+f)
	}
	if !out.Valid {
		fmt.Fprintln(stdout, "The configuration has errors; run pulse validate.")
	}
	return exitOK
}

func configGet(dir, file string, asJSON bool, stdout, stderr io.Writer) int {
	var err error
	var data []byte
	if !slicesContains(configFiles(dir), file) {
		err = fmt.Errorf("%s is not one of pulse's configuration files", file)
	} else {
		data, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
	}
	if asJSON {
		return finishJSON(stdout, stderr, struct {
			envelope
			File    string `json:"file"`
			Content string `json:"content"`
		}{newEnvelope("config", err), file, string(data)}, err)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	_, _ = stdout.Write(data)
	return exitOK
}

// configFiles lists the files pulse reads; secrets.env is never listed or served.
func configFiles(dir string) []string {
	files := []string{}
	for _, f := range []string{config.PulseFile, config.ChannelsFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			files = append(files, f)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, config.MonitorsDir))
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && (strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml")) {
			files = append(files, path.Join(config.MonitorsDir, n))
		}
	}
	return files
}

func slicesContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
