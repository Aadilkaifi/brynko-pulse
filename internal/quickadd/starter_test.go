// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package quickadd

import (
	"fmt"
	"testing"

	"github.com/Aadilkaifi/brynko-pulse/internal/monitor"
)

// pulse add and the published catalog suggest the same first threshold.
func TestDefaultThresholdsMatchTheCatalog(t *testing.T) {
	metric := map[string]string{"load": "per_cpu"}
	for typ, above := range defaultAbove {
		info, ok := monitor.Info(typ)
		if !ok {
			t.Fatalf("%s is not in the catalog", typ)
		}
		m := metric[typ]
		if m == "" {
			m = "used_percent"
		}
		if want := fmt.Sprintf("%s > %s", m, num(above)); info.Starter != want {
			t.Errorf("%s: catalog starter %q, pulse add uses %q", typ, info.Starter, want)
		}
	}
}
