// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package discover

import (
	"strings"
	"testing"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

func TestSuggest(t *testing.T) {
	cfg := &config.Config{Monitors: []config.Monitor{
		{ID: "root-disk", Type: "disk"}, {ID: "web", Type: "service", Service: "nginx"}, {ID: "https", Type: "port", Port: 443},
	}}
	inv := host.Inventory{Services: []string{"nginx", "redis-server"},
		Ports: []host.ListenPort{{Address: "0.0.0.0", Port: 80}, {Address: "::", Port: 443}, {Address: "127.0.0.1", Port: 3306},
			{Address: "0.0.0.0", Port: 22}, {Address: "0.0.0.0", Port: 5433}}}
	got, notes := Suggest(inv, []string{"api"}, cfg, "/")
	ids := []string{}
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if want := "memory cpu service-redis-server container-api port-80"; strings.Join(ids, " ") != want {
		t.Fatalf("got %v\nwant %s", ids, want)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "pulse add port") {
		t.Fatalf("other ports must be explained, not checked: %v", notes)
	}
}

func TestPortTarget(t *testing.T) {
	for in, want := range map[host.ListenPort]string{
		{Address: "0.0.0.0", Port: 80}: "80", {Address: "10.0.0.5", Port: 443}: "10.0.0.5:443", {Address: "fe80::1", Port: 443}: "[fe80::1]:443",
	} {
		if got := portTarget(in); got != want {
			t.Errorf("portTarget(%v) = %s, want %s", in, got, want)
		}
	}
}
