// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package linux

import (
	"os"
	"os/exec"
	"os/user"
	"testing"
)

func TestAccountCredential(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	cred, err := accountCredential(me.Username)
	if err != nil {
		t.Fatal(err)
	}
	if int(cred.Uid) != os.Getuid() {
		t.Fatalf("uid %d, want %d", cred.Uid, os.Getuid())
	}
	if _, err := accountCredential("no-such-account-for-pulse"); err == nil {
		t.Fatal("a missing account must be an error")
	}
}

func TestPrepareCommandLeavesOrdinaryUsersAlone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as root")
	}
	cmd := exec.Command("true")
	if err := (Platform{}).PrepareCommand(cmd); err != nil || cmd.SysProcAttr != nil || cmd.Env != nil {
		t.Fatalf("an ordinary user's command must not change: %v", err)
	}
}
