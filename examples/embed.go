// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package examples holds the example configuration. `pulse init` writes these
// same files, so the documentation and the starter set never drift apart.
package examples

import "embed"

// FS contains pulse.yaml, channels.yaml, secrets.env and monitors/*.yaml.
//
//go:embed pulse.yaml channels.yaml secrets.env monitors/*.yaml
var FS embed.FS
