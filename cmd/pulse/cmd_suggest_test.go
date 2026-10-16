// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Aadilkaifi/brynko-pulse/internal/monitor"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

func TestSuggestTargetsByType(t *testing.T) {
	inv := host.Inventory{
		Services: []string{"nginx", "mysql"},
		Ports:    []host.ListenPort{{Address: "0.0.0.0", Port: 443}, {Address: "::", Port: 443}, {Address: "127.0.0.1", Port: 3306}, {Port: 80}},
		Filesystems: []host.Filesystem{
			{Path: "/", Type: "ext4", Disk: host.Disk{TotalBytes: 30 << 30, UsedBytes: 22 << 30, FreeBytes: 8 << 30}},
			{Path: "/data", Disk: host.Disk{TotalBytes: 10 << 30, UsedBytes: 1 << 30, FreeBytes: 9 << 30}},
		},
	}
	got := suggestTargets(inv, []string{"web", "api"})
	want := map[string][]Target{
		"disk":      {{Value: "/", Note: "ext4, 30.0 GB, 73% used"}, {Value: "/data", Note: "10.0 GB, 10% used"}},
		"service":   {{Value: "nginx"}, {Value: "mysql"}},
		"container": {{Value: "api"}, {Value: "web"}},
		"port": {{Value: "80", Note: "listening on every address"}, {Value: "443", Note: "listening on every address"},
			{Value: "3306", Note: "listening on this server only"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if empty := suggestTargets(host.Inventory{}, nil); len(empty) != 4 || empty["disk"] == nil {
		t.Fatalf("every type is present even when empty: %+v", empty)
	}
}

// pulse schema publishes the monitor catalog as x-types, for forms.
func TestSchemaPublishesTheCatalog(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cmdSchema(nil, &out, &errOut); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var doc struct {
		Defs  map[string]any     `json:"$defs"`
		Types []monitor.TypeInfo `json:"x-types"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	// Compared as published: fields kept for pulse's own messages (Because) are not part of x-types.
	var want []monitor.TypeInfo
	if data, err := json.Marshal(monitor.Catalog); err != nil || json.Unmarshal(data, &want) != nil {
		t.Fatal("the catalog does not round-trip through JSON")
	}
	if doc.Defs["monitor"] == nil || !reflect.DeepEqual(doc.Types, want) {
		t.Fatalf("x-types does not match the catalog")
	}
}
