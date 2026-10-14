// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package platform

import "github.com/Aadilkaifi/brynko-pulse/internal/platform/windows"

var current Platform = windows.Platform{}
