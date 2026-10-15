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
		{ID: "root-disk", Type: "disk"}, {ID: "web", Type: "service", Service: "nginx"}, {ID: "pg", Type: "port", Port: 5432},
	}}
	inv := host.Inventory{Services: []string{"nginx", "redis-server"},
		Ports: []host.ListenPort{{Address: "0.0.0.0", Port: 80}, {Address: "127.0.0.1", Port: 5432}, {Address: "10.0.0.5", Port: 9000}, {Address: "fe80::1", Port: 7000}}}
	got := Suggest(inv, []string{"api"}, cfg, "/")
	ids := []string{}
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	want := "memory cpu service-redis-server container-api port-80 port-9000 port-7000"
	if strings.Join(ids, " ") != want {
		t.Fatalf("got %v\nwant %s", ids, want)
	}
	for _, s := range got {
		if s.ID == "port-9000" && !strings.Contains(s.YAML, "host: 10.0.0.5") {
			t.Errorf("a port bound to one address must be checked there:\n%s", s.YAML)
		}
		if s.ID == "port-7000" && !strings.Contains(s.YAML, "host: fe80::1") {
			t.Errorf("ipv6:\n%s", s.YAML)
		}
	}
}
