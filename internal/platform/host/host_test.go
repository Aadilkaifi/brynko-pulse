// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"testing"
	"time"
)

func TestParseUptimeReadsTheFirstField(t *testing.T) {
	got, err := ParseUptime("350735.47 234388.90\n")
	if err != nil || got != 350735*time.Second+470*time.Millisecond {
		t.Fatalf("ParseUptime = %v, %v", got, err)
	}
	for _, bad := range []string{"", "  \n", "soon 12", "-5 1"} {
		if _, err := ParseUptime(bad); err == nil {
			t.Errorf("ParseUptime(%q) accepted", bad)
		}
	}
}
