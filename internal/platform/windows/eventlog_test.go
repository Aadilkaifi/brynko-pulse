// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import "testing"

func TestEventText(t *testing.T) {
	xml := `<Event xmlns='http://schemas.microsoft.com/win/2004/08/events/event'><System><Provider Name='Service Control Manager'/><EventID Qualifiers='49152'>7034</EventID><Level>2</Level></System><EventData><Data Name='param1'>Print &amp; Fax</Data><Data Name='param2'>1</Data></EventData></Event>`
	want := "provider=Service Control Manager id=7034 level=error Print & Fax 1"
	if got := EventText(xml); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
