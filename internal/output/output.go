// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package output writes the --json contract shared by every command.
package output

import (
	"encoding/json"
	"io"
)

// APIVersion changes only when --json output changes in a way that breaks readers.
const APIVersion = 1

// JSON writes v as indented JSON followed by a newline.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
