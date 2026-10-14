// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Command pulse watches a server and sends alerts when something breaks.
package main

import (
	"errors"
	"flag"
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

const (
	exitOK      = 0
	exitInvalid = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "version":
		return cmdVersion(args[1:], stdout, stderr)
	case "schema":
		return cmdSchema(args[1:], stdout, stderr)
	case "init":
		return cmdInit(args[1:], stdout, stderr)
	case "validate":
		return cmdValidate(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "Unknown command %q.\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `pulse watches a server and alerts you when something breaks.

Usage:
  pulse <command> [flags]

Commands:
  init       Create the configuration folder with a starter setup
  validate   Check the configuration and explain any problems
  schema     Print the JSON Schema for the configuration files
  version    Show the version

Run "pulse <command> -h" for the flags of a command.
`)
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("pulse "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

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

	res, err := config.Init(config.InitOptions{Dir: *dir, ServerName: *name, RootDisk: p.RootDisk()})
	if *asJSON {
		out := struct {
			APIVersion int    `json:"api_version"`
			Command    string `json:"command"`
			OK         bool   `json:"ok"`
			Error      string `json:"error,omitempty"`
			config.InitResult
		}{output.APIVersion, "init", err == nil, "", res}
		if err != nil {
			out.Error = initErrorMessage(*dir, err)
		}
		if c := writeJSON(stdout, stderr, out); c != exitOK {
			return c
		}
		if err != nil {
			return exitInvalid
		}
		return exitOK
	}

	if err != nil {
		fmt.Fprintln(stderr, initErrorMessage(*dir, err))
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
`)
	return exitOK
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
		if c := writeJSON(stdout, stderr, struct {
			APIVersion int    `json:"api_version"`
			Command    string `json:"command"`
			OK         bool   `json:"ok"`
			config.Result
		}{output.APIVersion, "validate", res.OK(), res}); c != exitOK {
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

func writeJSON(stdout, stderr io.Writer, v any) int {
	if err := output.JSON(stdout, v); err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	return exitOK
}
