// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Command pulse watches a server and sends alerts when something breaks.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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

var commands = map[string]func(args []string, stdout, stderr io.Writer) int{
	"init":      cmdInit,
	"validate":  cmdValidate,
	"run":       cmdRun,
	"status":    cmdStatus,
	"history":   cmdHistory,
	"test":      cmdTest,
	"ping":      cmdPing,
	"silence":   cmdSilence,
	"add":       cmdAdd,
	"discover":  cmdDiscover,
	"install":   cmdInstall,
	"uninstall": cmdUninstall,
	"schema":    cmdSchema,
	"version":   cmdVersion,
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
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "Unknown command %q.\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
	return cmd(args[1:], stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `pulse watches a server and alerts you when something breaks.

Usage:
  pulse <command> [flags]

Set up:
  init       Create the configuration folder with a starter setup
  validate   Check the configuration and explain any problems
  discover   Find services, containers and ports to monitor, and suggest monitors
  add        Add a monitor with one command, for example: pulse add disk /data --above 85
  test       Send a test alert, or run one monitor once

Run:
  install    Run pulse as a background service that starts at boot
  uninstall  Stop and remove the service (keeps the configuration)
  run        Start monitoring in this terminal (runs until stopped)
  status     Show every monitor and channel right now
  history    Show recent alerts
  silence    Pause alerts for a while, for example during a deploy
  ping       Record a heartbeat, for jobs that check in

Other:
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
