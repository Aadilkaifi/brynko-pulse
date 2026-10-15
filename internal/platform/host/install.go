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
	// ContainerMonitors is how many container monitors are configured, so
	// install can warn when they will lack access.
	ContainerMonitors int
	// JournalMonitors and LogFiles describe log monitors, so install can give
	// read access to the journal and warn about files the service cannot read.
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
