// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package linux

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

// commandEnv is what a command check sees when pulse drops rights, so nothing
// from an administrator's shell (sudo variables, tokens) reaches it.
var commandEnv = []string{
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	"HOME=/",
	"LANG=C.UTF-8",
}

// PrepareCommand runs command checks as the service account whenever pulse
// itself has root rights, for example `sudo pulse test monitor`. A check then
// has exactly the rights it has in the service, and never root.
func (Platform) PrepareCommand(cmd *exec.Cmd) error {
	if os.Geteuid() != 0 {
		return nil
	}
	cred, err := accountCredential(serviceUser)
	if err != nil {
		return fmt.Errorf("command checks never run as root, and the %s account is not set up (%v). Install pulse first with: sudo pulse install", serviceUser, err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	cmd.Env = commandEnv
	return nil
}

func accountCredential(name string) (*syscall.Credential, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, err
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, err
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, err
	}
	cred := &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	ids, err := u.GroupIds()
	if err != nil {
		return nil, err
	}
	for _, g := range ids {
		if n, err := strconv.ParseUint(g, 10, 32); err == nil {
			cred.Groups = append(cred.Groups, uint32(n))
		}
	}
	return cred, nil
}
