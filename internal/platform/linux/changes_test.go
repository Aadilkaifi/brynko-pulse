// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux

package linux

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
)

func TestJournalEntriesThatAreChanges(t *testing.T) {
	sudo := `{"__CURSOR":"s=1;i=2","__REALTIME_TIMESTAMP":"1772460000000000","SYSLOG_IDENTIFIER":"sudo","MESSAGE":"ubuntu : TTY=pts/0 ; PWD=/srv ; USER=root ; COMMAND=/usr/bin/systemctl restart nginx"}`
	c, cursor, ok := journalChange([]byte(sudo))
	if !ok || cursor != "s=1;i=2" || c.Kind != host.ChangeSudo || c.What != "systemctl restart nginx" ||
		!c.At.Equal(time.UnixMicro(1772460000000000).UTC()) {
		t.Fatalf("%+v %q", c, cursor)
	}
	ssh := `{"__CURSOR":"s=1;i=3","__REALTIME_TIMESTAMP":"1772460000000000","SYSLOG_IDENTIFIER":"sshd-session","MESSAGE":"Accepted publickey for deploy from 10.0.0.9 port 4000 ssh2"}`
	if c, _, _ := journalChange([]byte(ssh)); c.Kind != host.ChangeLogin || c.Who != "deploy" {
		t.Fatalf("%+v", c)
	}
	// An entry that is not a change still moves the cursor on; one that is not text is skipped.
	other := `{"__CURSOR":"s=1;i=4","__REALTIME_TIMESTAMP":"1","SYSLOG_IDENTIFIER":"sudo","MESSAGE":[1,2,3]}`
	if c, cursor, ok := journalChange([]byte(other)); !ok || cursor != "s=1;i=4" || c.Kind != "" {
		t.Fatalf("%+v %q %v", c, cursor, ok)
	}
	if _, _, ok := journalChange([]byte("not json")); ok {
		t.Fatal("not an entry")
	}
}

const aptBlock = "\nStart-Date: %s  10:00:00\nCommandline: apt-get install -y %[2]s\nRequested-By: ubuntu (1000)\nInstall: %[2]s:amd64 (1.0)\nEnd-Date: %[1]s  10:00:05\n"

func TestAptHistoryIsReadOnceAndAcrossRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.log")
	day := time.Now().Format("2006-01-02")
	appendTo := func(p, s string) {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(s)
		f.Close()
	}
	a := aptChanges{path: path}
	var got []string
	emit := func(c host.Change) { got = append(got, c.What) }

	appendTo(path, fmt.Sprintf(aptBlock, day, "htop"))
	appendTo(path, "\nStart-Date: "+day+"  11:00:00\nCommandline: apt-get install -y jq\n") // still running
	ino, off := a.read(0, 0, emit)
	if len(got) != 1 || got[0] != "installed htop" {
		t.Fatalf("%v", got)
	}
	ino, off = a.read(ino, off, emit)
	if len(got) != 1 {
		t.Fatalf("nothing new is read twice: %v", got)
	}

	// apt finishes the block, then the file is rotated and a new one started.
	appendTo(path, "Install: jq:amd64 (1.6)\nEnd-Date: "+day+"  11:00:03\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendTo(path, fmt.Sprintf(aptBlock, day, "curl"))
	ino, off = a.read(ino, off, emit)
	if len(got) != 3 || got[1] != "installed jq" || got[2] != "installed curl" {
		t.Fatalf("the rotated file is finished first: %v", got)
	}
	if c := fmt.Sprintf("%d:%d", ino, off); c == "0:0" {
		t.Fatal("a position is kept")
	}
	if i, o := parseFileCursor(fmt.Sprintf("%d:%d", ino, off)); i != ino || o != off {
		t.Fatal("the cursor reads back")
	}
	if i, o := parseFileCursor("junk"); i != 0 || o != 0 {
		t.Fatal("a bad cursor starts over")
	}
}
