// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package platform

import "github.com/Aadilkaifi/brynko-pulse/internal/platform/linux"

var current Platform = linux.Platform{}
