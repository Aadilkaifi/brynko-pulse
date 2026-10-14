// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesValidConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "brynko-pulse")
	res, err := Init(InitOptions{Dir: dir, ServerName: "db_1.prod", RootDisk: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 4 {
		t.Fatalf("created %v, want 4 files", res.Created)
	}
	got := Validate(dir)
	if !got.OK() {
		t.Fatalf("fresh init has errors: %+v", got.Errors)
	}
	data, _ := os.ReadFile(filepath.Join(dir, PulseFile))
	if !strings.Contains(string(data), "server_name: db_1.prod") {
		t.Fatalf("server name not written:\n%s", data)
	}
}

func TestInitNeverOverwrites(t *testing.T) {
	dir := writeConfig(t, map[string]string{ChannelsFile: "channels: {}\n"})
	_, err := Init(InitOptions{Dir: dir, ServerName: "x"})
	var exists *ExistsError
	if !errors.As(err, &exists) || !errors.Is(err, ErrAlreadySetUp) {
		t.Fatalf("err = %v, want ExistsError", err)
	}
	if _, err := os.Stat(filepath.Join(dir, PulseFile)); !os.IsNotExist(err) {
		t.Fatal("Init wrote pulse.yaml even though it refused")
	}
}

func TestInitWindowsDisk(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(InitOptions{Dir: dir, ServerName: "win", RootDisk: `C:\`}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, MonitorsDir, "system.yaml"))
	if !strings.Contains(string(data), `path: 'C:\'`) {
		t.Fatalf("disk path not replaced:\n%s", data)
	}
	if res := Validate(dir); !res.OK() {
		t.Fatalf("errors: %+v", res.Errors)
	}
}

func TestSanitizeServerName(t *testing.T) {
	for in, want := range map[string]string{
		"web-1":                 "web-1",
		"My Laptop (home)":      "My-Laptop-home",
		"":                      "server",
		"---":                   "server",
		strings.Repeat("a", 80): strings.Repeat("a", 63),
	} {
		if got := SanitizeServerName(in); got != want {
			t.Errorf("SanitizeServerName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The commented-out samples in the examples must stay valid when uncommented,
// or first-time users copy broken config.
func TestCommentedExamplesAreValid(t *testing.T) {
	for _, v := range []string{"PULSE_SMTP_PASSWORD", "PULSE_TELEGRAM_TOKEN", "PULSE_SLACK_WEBHOOK",
		"PULSE_WHATSAPP_TOKEN", "PULSE_WEBHOOK_SECRET"} {
		t.Setenv(v, "x")
	}
	src := filepath.Join("..", "..", "examples")
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	uncomment := func(s, emptyLine, header string) string {
		s = strings.Replace(s, emptyLine, header, 1)
		var out []string
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, "#  ") {
				line = line[1:]
			}
			out = append(out, line)
		}
		return strings.Join(out, "\n")
	}
	pulse := strings.Replace(read(PulseFile), "# notify: [ops-email]", "notify: [ops-email]", 1)
	dir := writeConfig(t, map[string]string{
		PulseFile:                     pulse,
		ChannelsFile:                  uncomment(read(ChannelsFile), "channels: {}", "channels:"),
		"monitors/system.yaml":        read("monitors/system.yaml"),
		"monitors/more-examples.yaml": uncomment(read("monitors/more-examples.yaml"), "monitors: []", "monitors:"),
	})
	res := Validate(dir)
	if !res.OK() {
		t.Fatalf("uncommented examples have errors: %+v", res.Errors)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("uncommented examples have warnings: %+v", res.Warnings)
	}
}
