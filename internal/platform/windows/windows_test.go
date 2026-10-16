// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package windows

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// Files below a secured folder must keep the folder's permissions, never end up with a NULL DACL
// (which grants everyone full control). The owner of a folder may change its permissions, so this
// runs without administrator rights.
func TestSetTreeACLLeavesNoFileOpenToEveryone(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "monitors"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"channels.yaml", filepath.Join("monitors", "web.yaml")} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	me, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// SYSTEM and Administrators as in pulse, plus the current user so the test can clean up.
	sddl := adminOnlySDDL + "(A;OICI;FA;;;" + me.User.Sid.String() + ")"
	if err := setTreeACL(dir, sddl, ""); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"channels.yaml", "monitors", filepath.Join("monitors", "web.yaml")} {
		p := filepath.Join(dir, f)
		sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if dacl == nil {
			t.Fatalf("%s has a NULL DACL: everyone may change it", f)
		}
		if dacl.AceCount == 0 {
			t.Fatalf("%s inherited nothing from its folder", f)
		}
		if who, err := grantedToOthers(p, writeRights); err != nil || (who != "" && who != me.User.Sid.String()) {
			t.Fatalf("%s can be changed by %q (%v)", f, who, err)
		}
	}
}

// The page file is measured from the page files themselves (what Task Manager shows), never from the
// commit charge, which reads as a full page file on a busy server.
func TestPageFilesAreMeasuredDirectly(t *testing.T) {
	total, used, err := pageFiles()
	if err != nil {
		t.Fatal(err)
	}
	if used > total {
		t.Fatalf("used %d is more than the page files' size %d", used, total)
	}
	t.Logf("page files: %d MB, %d MB in use", total>>20, used>>20)
}
