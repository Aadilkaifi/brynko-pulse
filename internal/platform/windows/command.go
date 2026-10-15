// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import "os/exec"

// PrepareCommand leaves the command as it is. The service runs checks as its
// virtual account; `pulse test` runs them as whoever started it.
func (Platform) PrepareCommand(*exec.Cmd) error { return nil }
