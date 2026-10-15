// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/output"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/version"
	"github.com/Aadilkaifi/brynko-pulse/schema"
)

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("version", stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(args) != nil {
		return exitUsage
	}
	info := version.Get()
	if *asJSON {
		return writeJSON(stdout, stderr, struct {
			APIVersion int `json:"api_version"`
			version.Info
			ConfigVersion int `json:"config_version"`
		}{output.APIVersion, info, schema.Version})
	}
	fmt.Fprintf(stdout, "pulse %s (%s, %s/%s)\n", info.Version, info.Go, info.OS, info.Arch)
	return exitOK
}

func cmdSchema(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("schema", stderr)
	if fs.Parse(args) != nil {
		return exitUsage
	}
	if _, err := stdout.Write(schema.JSON); err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	return exitOK
}

func cmdInit(args []string, stdout, stderr io.Writer) int {
	host, _ := os.Hostname()
	p := platform.Current()
	fs := newFlags("init", stderr)
	dir := fs.String("config", p.ConfigDir(), "configuration directory to create")
	name := fs.String("server-name", host, "name shown in alerts (defaults to this machine's host name)")
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(args) != nil {
		return exitUsage
	}

	res, err := initConfig(p, *dir, *name)
	if *asJSON {
		return finishJSON(stdout, stderr, struct {
			envelope
			config.InitResult
		}{newEnvelope("init", err), res}, err)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	fmt.Fprintf(stdout, "Created %s\n", res.Dir)
	for _, f := range res.Created {
		fmt.Fprintf(stdout, "  %s\n", f)
	}
	fmt.Fprint(stdout, `
Next:
  1. Open channels.yaml and add where alerts should go (email, Telegram, Slack...).
  2. Add that channel's name to "notify" in pulse.yaml.
  3. Run: pulse validate
  4. Check the channel works: pulse test channel <name>
`)
	return exitOK
}

// initConfig writes the starter configuration and locks the folder down.
func initConfig(p platform.Platform, dir, serverName string) (config.InitResult, error) {
	res, err := config.Init(config.InitOptions{Dir: dir, ServerName: serverName, RootDisk: p.RootDisk()})
	if err == nil {
		err = p.SecureConfig(dir)
	}
	if err != nil {
		err = errors.New(initErrorMessage(dir, err))
	}
	return res, err
}

func initErrorMessage(dir string, err error) string {
	var exists *config.ExistsError
	switch {
	case errors.As(err, &exists):
		return fmt.Sprintf("pulse is already set up in %s (%s). Nothing was changed. Edit those files, then run pulse validate.", dir, exists.Error())
	case errors.Is(err, fs.ErrPermission):
		return fmt.Sprintf("Cannot write to %s: permission denied. Run as root or Administrator, or choose another folder with --config.", dir)
	default:
		return fmt.Sprintf("Could not create the configuration in %s: %v", dir, err)
	}
}

func cmdValidate(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("validate", stderr)
	dir := fs.String("config", platform.Current().ConfigDir(), "configuration directory")
	asJSON := fs.Bool("json", false, "print JSON")
	if fs.Parse(args) != nil {
		return exitUsage
	}

	res := config.Validate(*dir)
	code := exitOK
	if !res.OK() {
		code = exitInvalid
	}

	if *asJSON {
		e := newEnvelope("validate", nil)
		e.OK = res.OK()
		if c := writeJSON(stdout, stderr, struct {
			envelope
			config.Result
		}{e, res}); c != exitOK {
			return c
		}
		return code
	}

	fmt.Fprintf(stdout, "Checked %s\n", res.Dir)
	for _, is := range res.Errors {
		fmt.Fprintf(stdout, "error    %s\n", formatIssue(is))
	}
	for _, is := range res.Warnings {
		fmt.Fprintf(stdout, "warning  %s\n", formatIssue(is))
	}
	switch {
	case !res.OK():
		fmt.Fprintf(stdout, "The configuration has %d error(s). Fix them and run pulse validate again.\n", len(res.Errors))
	case len(res.Warnings) > 0:
		fmt.Fprintf(stdout, "The configuration is valid, with %d warning(s).\n", len(res.Warnings))
	default:
		fmt.Fprintln(stdout, "The configuration is valid.")
	}
	return code
}

func formatIssue(is config.Issue) string {
	loc := is.File
	if is.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, is.Line)
	}
	if loc == "" {
		return is.Message
	}
	return loc + "  " + is.Message
}
