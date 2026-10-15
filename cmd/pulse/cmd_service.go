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
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
	"github.com/Aadilkaifi/brynko-pulse/internal/runner"
	"github.com/Aadilkaifi/brynko-pulse/internal/setup"
)

// Replaced in tests, so they never touch the real service manager.
var (
	stdin       io.Reader = os.Stdin
	platformFor           = platform.Current
)

const installTimeout = 3 * time.Minute

func cmdInstall(args []string, stdout, stderr io.Writer) int {
	p := platformFor()
	fs := newFlags("install", stderr)
	cfgDir := fs.String("config", p.ConfigDir(), "configuration directory (created with a starter setup if missing)")
	dataDir := fs.String("data", p.DataDir(), "where state and alert history are kept")
	allowDocker := fs.Bool("allow-docker", false, "let the service use Docker, for container monitors (equivalent to root)")
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(args) != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return exitUsage
	}

	res, initRes, err := install(p, *cfgDir, *dataDir, *allowDocker)
	if *asJSON {
		return finishJSON(stdout, stderr, struct {
			envelope
			ConfigCreated bool `json:"config_created"`
			host.InstallResult
		}{newEnvelope("install", err), len(initRes.Created) > 0, res}, err)
	}
	if len(initRes.Created) > 0 {
		fmt.Fprintf(stdout, "Created the starter configuration in %s\n", initRes.Dir)
	}
	printSteps(stdout, res.Steps)
	for _, w := range res.Warnings {
		fmt.Fprintln(stdout, "warning  "+w)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	fmt.Fprintf(stdout, "\npulse is running as the %s service.\n", res.Service)
	for _, n := range res.Next {
		fmt.Fprintln(stdout, "  "+n)
	}
	return exitOK
}

// install checks everything it can before changing the system, so a bad
// configuration never leaves a half-installed service behind.
func install(p platform.Platform, cfgDir, dataDir string, allowDocker bool) (host.InstallResult, config.InitResult, error) {
	var initRes config.InitResult
	cfgDir, dataDir, err := absDirs(cfgDir, dataDir)
	if err != nil {
		return host.InstallResult{}, initRes, err
	}
	if _, err := os.Stat(filepath.Join(cfgDir, config.PulseFile)); errors.Is(err, os.ErrNotExist) {
		hostname, _ := os.Hostname()
		if initRes, err = initConfig(p, cfgDir, hostname); err != nil {
			return host.InstallResult{}, initRes, err
		}
	}
	cfg, err := runner.Load(runner.Options{ConfigDir: cfgDir, Platform: p})
	if err != nil {
		return host.InstallResult{}, initRes, err
	}
	opts := host.InstallOptions{ConfigDir: cfgDir, DataDir: dataDir, AllowDocker: allowDocker}
	for _, m := range cfg.Monitors {
		switch {
		case !m.IsEnabled():
		case m.Type == "container":
			opts.ContainerMonitors++
		case m.Type == "log" && m.Source != nil && m.Source.Journal != nil:
			opts.JournalMonitors++
		case m.Type == "log" && m.Source != nil && m.Source.File != "":
			opts.LogFiles = append(opts.LogFiles, m.Source.File)
		}
	}
	src, err := os.Executable()
	if err == nil {
		src, err = filepath.EvalSymlinks(src)
	}
	if err != nil {
		return host.InstallResult{}, initRes, fmt.Errorf("cannot find the running pulse binary: %w", err)
	}

	ctx, cancel := signalContext(installTimeout)
	defer cancel()
	opts.Source = src
	res, err := p.Install(ctx, opts)
	return res, initRes, err
}

func cmdUninstall(args []string, stdout, stderr io.Writer) int {
	p := platformFor()
	fs := newFlags("uninstall", stderr)
	cfgDir := fs.String("config", p.ConfigDir(), "configuration directory")
	dataDir := fs.String("data", p.DataDir(), "where state and alert history are kept")
	purge := fs.Bool("purge", false, "also delete the configuration, secrets and alert history")
	yes := fs.Bool("yes", false, "with --purge: do not ask for confirmation")
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(args) != nil {
		return exitUsage
	}
	if fs.NArg() != 0 || (*yes && !*purge) {
		fs.Usage()
		return exitUsage
	}

	steps, err := uninstall(p, *cfgDir, *dataDir, *purge, *yes, *asJSON, stdout)
	if *asJSON {
		return finishJSON(stdout, stderr, struct {
			envelope
			Purged bool     `json:"purged"`
			Steps  []string `json:"steps"`
		}{newEnvelope("uninstall", err), *purge && err == nil, steps}, err)
	}
	printSteps(stdout, steps)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	switch {
	case *purge:
		fmt.Fprintln(stdout, "pulse is removed, with its configuration and history.")
	case len(steps) == 0:
		fmt.Fprintln(stdout, "pulse was not installed as a service. Nothing was changed.")
	default:
		fmt.Fprintf(stdout, "pulse is removed. The configuration in %s and the history in %s were kept;\nrun pulse uninstall --purge to delete them too.\n", *cfgDir, *dataDir)
	}
	return exitOK
}

func uninstall(p platform.Platform, cfgDir, dataDir string, purge, yes, asJSON bool, stdout io.Writer) ([]string, error) {
	steps := []string{}
	cfgDir, dataDir, err := absDirs(cfgDir, dataDir)
	if err != nil {
		return steps, err
	}
	if purge && !yes {
		if asJSON {
			return steps, errors.New("--purge deletes the configuration and history; add --yes to confirm when using --json")
		}
		fmt.Fprintf(stdout, "This deletes %s and %s, including secrets and alert history.\nType delete to confirm: ", cfgDir, dataDir)
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if strings.TrimSpace(line) != "delete" {
			return steps, errors.New("not confirmed. Nothing was changed")
		}
	}

	ctx, cancel := signalContext(installTimeout)
	defer cancel()
	res, err := p.Uninstall(ctx, host.UninstallOptions{ConfigDir: cfgDir, DataDir: dataDir, Purge: purge})
	steps = append(steps, res.Steps...)
	if err != nil || !purge {
		return steps, err
	}
	purged, err := setup.Purge(cfgDir, dataDir)
	return append(steps, purged...), err
}

func absDirs(cfgDir, dataDir string) (string, string, error) {
	var err error
	if cfgDir, err = filepath.Abs(cfgDir); err != nil {
		return "", "", err
	}
	if dataDir, err = filepath.Abs(dataDir); err != nil {
		return "", "", err
	}
	if err := setup.CheckDir("configuration", cfgDir); err != nil {
		return "", "", err
	}
	return cfgDir, dataDir, setup.CheckDir("data", dataDir)
}

func signalContext(limit time.Duration) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	ctx, cancel := context.WithTimeout(ctx, limit)
	return ctx, func() { cancel(); stop() }
}

func printSteps(w io.Writer, steps []string) {
	for _, s := range steps {
		fmt.Fprintln(w, "  done  "+s)
	}
}
