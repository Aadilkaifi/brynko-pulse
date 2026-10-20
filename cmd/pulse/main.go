// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Command pulse watches a server and sends alerts when something breaks.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	_ "time/tzdata" // the same time zones on every system, for maintenance windows

	"github.com/Aadilkaifi/brynko-pulse/internal/output"
)

const (
	exitOK      = 0
	exitInvalid = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// command is one pulse subcommand. The same table drives `pulse help`, each
// command's -h and the generated docs/cli.md, so they cannot drift apart.
type command struct {
	name    string
	section string
	summary string
	usage   string // what follows "pulse " in the synopsis
	details string // examples or notes, shown in -h
	run     func(args []string, stdout, stderr io.Writer) int
}

var commands []command

// Filled in init: the run functions read this table for their help text.
func init() {
	commands = []command{
		{name: "init", section: "Set up", summary: "Create the configuration folder with a starter setup",
			usage: "init [flags]", run: cmdInit},
		{name: "validate", section: "Set up", summary: "Check the configuration and explain any problems",
			usage: "validate [flags]", run: cmdValidate},
		{name: "discover", section: "Set up", summary: "Find services, containers and ports to monitor, and suggest monitors",
			usage: "discover [--apply] [flags]", run: cmdDiscover},
		{name: "suggest", section: "Set up", summary: "List what a monitor could watch here: filesystems, services, containers, ports",
			usage: "suggest [flags]", details: suggestDetails, run: cmdSuggest},
		{name: "add", section: "Set up", summary: "Add a monitor with one command",
			usage: "add <type> [target] [flags] [-- program args...]", details: addDetails, run: cmdAdd},
		{name: "config", section: "Set up", summary: "Show or change the configuration, one file at a time",
			usage: "config show | get <file> | set <file> | delete <file> [flags]", details: configDetails, run: cmdConfig},
		{name: "secret", section: "Set up", summary: "Store or remove a secret used by channels",
			usage: "secret set <NAME> | secret delete <NAME> [flags]", details: secretDetails, run: cmdSecret},
		{name: "test", section: "Set up", summary: "Send a test alert, or run one monitor once",
			usage: "test channel <name> | test monitor <id> [flags]", details: testDetails, run: cmdTest},
		{name: "install", section: "Run", summary: "Run pulse as a background service that starts at boot",
			usage: "install [flags]", run: cmdInstall},
		{name: "uninstall", section: "Run", summary: "Stop and remove the service (keeps the configuration)",
			usage: "uninstall [--purge] [flags]", run: cmdUninstall},
		{name: "run", section: "Run", summary: "Start monitoring in this terminal (runs until stopped)",
			usage: "run [flags]", run: cmdRun},
		{name: "status", section: "Run", summary: "Show every monitor and channel right now",
			usage: "status [flags]", run: cmdStatus},
		{name: "history", section: "Run", summary: "Show recent alerts",
			usage: "history [flags]", run: cmdHistory},
		{name: "silence", section: "Run", summary: "Pause alerts for a while, for example during a deploy",
			usage:   "silence <monitor-id|all> <duration> [--reason text] | silence list | silence clear <monitor-id|all>",
			details: silenceDetails, run: cmdSilence},
		{name: "fix", section: "Run", summary: "Approve or dismiss a fix that self-healing prepared",
			usage: "fix approve <monitor-id> | fix dismiss <monitor-id> [--id fix-id] [flags]", details: fixDetails, run: cmdFix},
		{name: "ping", section: "Run", summary: "Record a heartbeat, for jobs that check in",
			usage: "ping <heartbeat-monitor-id> [flags]", run: cmdPing},
		{name: "schema", section: "Other", summary: "Print the JSON Schema for the configuration files",
			usage: "schema", run: cmdSchema},
		{name: "version", section: "Other", summary: "Show the version",
			usage: "version [flags]", run: cmdVersion},
	}
}

func findCommand(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stdout)
		return exitOK
	}
	cmd, ok := findCommand(args[0])
	if !ok {
		fmt.Fprintf(stderr, "Unknown command %q.\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
	return cmd.run(args[1:], stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprint(w, "pulse watches a server and alerts you when something breaks.\n\nUsage:\n  pulse <command> [flags]\n")
	section := ""
	for _, c := range commands {
		if c.section != section {
			section = c.section
			fmt.Fprintf(w, "\n%s:\n", section)
		}
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprint(w, "\nRun \"pulse <command> -h\" for the flags of a command.\n")
}

// newFlags returns the flag set for a command, with help built from the
// command table.
func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("pulse "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	c, _ := findCommand(name)
	fs.Usage = func() { commandHelp(stderr, c, fs) }
	return fs
}

func commandHelp(w io.Writer, c command, fs *flag.FlagSet) {
	fmt.Fprintf(w, "%s.\n\nUsage:\n  pulse %s\n", c.summary, c.usage)
	if c.details != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimRight(c.details, "\n"))
	}
	hasFlags := false
	fs.VisitAll(func(*flag.Flag) { hasFlags = true })
	if hasFlags {
		fmt.Fprint(w, "\nFlags:\n")
		fs.SetOutput(w)
		fs.PrintDefaults()
	}
}

// envelope is the start of every --json answer.
type envelope struct {
	APIVersion int    `json:"api_version"`
	Command    string `json:"command"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

func newEnvelope(command string, err error) envelope {
	e := envelope{APIVersion: output.APIVersion, Command: command, OK: err == nil}
	if err != nil {
		e.Error = err.Error()
	}
	return e
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	if err := output.JSON(stdout, v); err != nil {
		fmt.Fprintln(stderr, err)
		return exitInvalid
	}
	return exitOK
}

// finishJSON writes v and returns the exit code that matches err.
func finishJSON(stdout, stderr io.Writer, v any, err error) int {
	if c := writeJSON(stdout, stderr, v); c != exitOK {
		return c
	}
	if err != nil {
		return exitInvalid
	}
	return exitOK
}
