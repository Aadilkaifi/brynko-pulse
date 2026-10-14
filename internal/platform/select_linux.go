// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Linux is the supported target; other Unix systems build against it for development only.

//go:build !windows

package platform

import "github.com/Aadilkaifi/brynko-pulse/internal/platform/linux"

var current Platform = linux.Platform{}
