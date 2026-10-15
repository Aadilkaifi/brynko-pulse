// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

// Package discover proposes monitors for what runs on a server and is not
// watched yet. It only proposes; config.Apply writes what the user accepts.
package discover

import (
	"fmt"
	"strconv"

	"github.com/Aadilkaifi/brynko-pulse/internal/config"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
	"github.com/Aadilkaifi/brynko-pulse/internal/quickadd"
)

// Suggestion is one proposed monitor and the reason for it.
type Suggestion struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Why  string `json:"why"`
	File string `json:"file"`
	YAML string `json:"yaml"`
}

// Only web ports get a suggested port check: web servers accept and drop a bare
// connection quietly, while databases, SSH and brokers log it as a failed client
// (MySQL can even block the address). Everything else is watched through its
// service or container monitor.
var webPorts = map[int]bool{80: true, 443: true}

func portTarget(p host.ListenPort) string {
	switch p.Address {
	case "", "0.0.0.0", "::", "127.0.0.1":
		return strconv.Itoa(p.Port)
	}
	return joinHostPort(p.Address, p.Port)
}

// Suggest returns monitors for inv and containers that cfg does not cover yet,
// and notes about what it left out on purpose. rootDisk is the system disk,
// for when no disk monitor exists.
func Suggest(inv host.Inventory, containers []string, cfg *config.Config, rootDisk string) ([]Suggestion, []string) {
	have := watched(cfg)
	var out []Suggestion
	var skipped []string
	add := func(s quickadd.Spec, key, why string) {
		if have[key] {
			return
		}
		r, err := quickadd.Build(s)
		if err != nil || have["id:"+r.ID] {
			return
		}
		have[key], have["id:"+r.ID] = true, true
		out = append(out, Suggestion{ID: r.ID, Type: s.Type, Why: why, File: r.Path, YAML: string(r.YAML)})
	}

	for _, t := range []string{"disk", "memory", "cpu"} {
		target := ""
		if t == "disk" {
			target = rootDisk
		}
		add(quickadd.Spec{Type: t, Target: target}, "type:"+t, "every server needs it")
	}
	for _, s := range inv.Services {
		add(quickadd.Spec{Type: "service", Target: s}, "service:"+s, "the "+s+" service is running")
	}
	for _, c := range containers {
		add(quickadd.Spec{Type: "container", Target: c}, "container:"+c, "the "+c+" container is running")
	}
	for _, p := range inv.Ports {
		if !webPorts[p.Port] || have["port:"+strconv.Itoa(p.Port)] {
			continue
		}
		add(quickadd.Spec{Type: "port", Target: portTarget(p)}, "port:"+strconv.Itoa(p.Port),
			fmt.Sprintf("the web server listens on port %d", p.Port))
	}
	others := 0
	for _, p := range inv.Ports {
		if !webPorts[p.Port] {
			others++
		}
	}
	if others > 0 {
		skipped = append(skipped, "other listening ports get no port check: their service or container monitor covers them, "+
			"and a bare connection shows up in many programs' logs as a failed client. Add one with: pulse add port <number>")
	}
	return out, skipped
}

func joinHostPort(addr string, port int) string {
	for _, c := range addr {
		if c == ':' {
			return "[" + addr + "]:" + strconv.Itoa(port)
		}
	}
	return addr + ":" + strconv.Itoa(port)
}

// watched lists what the configuration already covers.
func watched(cfg *config.Config) map[string]bool {
	have := map[string]bool{}
	if cfg == nil {
		return have
	}
	for _, m := range cfg.Monitors {
		have["id:"+m.ID] = true
		switch m.Type {
		case "disk", "memory", "cpu":
			have["type:"+m.Type] = true
		case "service":
			have["service:"+m.Service] = true
		case "container":
			have["container:"+m.Container] = true
		case "port":
			have["port:"+strconv.Itoa(m.Port)] = true
		}
	}
	return have
}
