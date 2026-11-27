// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"context"
	"errors"
	"log/slog"
)

// ServiceName is the name pulse registers with systemd or the Service Control Manager.
const ServiceName = "brynko-pulse"

// ErrNotAdmin means the action needs root or Administrator rights.
var ErrNotAdmin = errors.New("this needs root or Administrator rights")

// InstallOptions controls Install.
type InstallOptions struct {
	Source      string // the running binary, copied into place
	ConfigDir   string
	DataDir     string
	AllowDocker bool // give the service user access to Docker, where Docker is installed (Linux)
	// AllowJournal lets the service user read the whole systemd journal (Linux), for the change
	// timeline's sudo commands and sign-ins and for journal log monitors.
	AllowJournal bool
	// ContainerMonitors is how many container monitors are configured, so
	// install can warn when they will lack access.
	ContainerMonitors int
	// JournalMonitors and LogFiles describe log monitors, so install can warn
	// about the ones that will not be able to read their source.
	JournalMonitors int
	LogFiles        []string
}

// InstallResult describes what Install set up.
type InstallResult struct {
	Binary   string   `json:"binary"`
	Service  string   `json:"service"`
	Account  string   `json:"account"`
	Steps    []string `json:"steps"`
	Warnings []string `json:"warnings"`
	Next     []string `json:"next"` // what the user can do now, in plain words
}

// HealRights describes what SetHealRights changed, in plain words.
type HealRights struct {
	Steps    []string `json:"steps"`
	Warnings []string `json:"warnings"`
}

// ErrHealNotAllowed means the service lacks the right to restart a service:
// SetHealRights has not granted it (yet).
var ErrHealNotAllowed = errors.New("pulse is not allowed to restart this service")

// UninstallOptions controls Uninstall.
type UninstallOptions struct {
	ConfigDir string
	DataDir   string
	Purge     bool // also remove the service account
}

// UninstallResult lists what Uninstall removed.
type UninstallResult struct {
	Steps []string `json:"steps"`
}

// ServiceFunc runs pulse until ctx ends. A value on reload asks it to reread the configuration.
type ServiceFunc func(ctx context.Context, reload <-chan struct{}, log *slog.Logger) error
