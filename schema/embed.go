// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package schema embeds the published JSON Schema for pulse configuration.
package schema

import _ "embed"

// ID is the schema's $id.
const ID = "urn:brynko-pulse:config:1"

// Version is the config format version this schema describes.
const Version = 1

// JSON is the schema document.
//
//go:embed pulse.schema.json
var JSON []byte
