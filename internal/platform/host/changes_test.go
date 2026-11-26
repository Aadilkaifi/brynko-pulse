// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 3, 2, 14, 32, 0, 0, time.UTC)

func TestParseSudo(t *testing.T) {
	c, ok := ParseSudo(at, "ubuntu : TTY=pts/0 ; PWD=/home/ubuntu ; USER=root ; COMMAND=/usr/bin/vim /etc/nginx/sites-enabled/default")
	if !ok || c.Kind != ChangeSudo || c.Who != "ubuntu" || c.What != "vim /etc/nginx/sites-enabled/default" || c.Detail != "in /home/ubuntu" {
		t.Fatalf("%+v", c)
	}
	c, ok = ParseSudo(at, "deploy : PWD=/srv ; USER=www-data ; COMMAND=/usr/bin/php artisan migrate")
	if !ok || c.Detail != "as www-data, in /srv" {
		t.Fatalf("%+v", c)
	}
	for _, line := range []string{
		"ubuntu : 3 incorrect password attempts ; TTY=pts/0 ; PWD=/home/ubuntu ; USER=root ; COMMAND=/usr/bin/su",
		"ubuntu : command not allowed ; TTY=pts/0 ; PWD=/ ; USER=root ; COMMAND=/usr/bin/rm -rf /",
		"pam_unix(sudo:session): session opened for user root(uid=0) by ubuntu(uid=1000)",
	} {
		if _, ok := ParseSudo(at, line); ok {
			t.Errorf("not a change: %s", line)
		}
	}
}

func TestParseLogin(t *testing.T) {
	c, ok := ParseLogin(at, "Accepted publickey for ubuntu from 203.0.113.5 port 52114 ssh2: ED25519 SHA256:abc")
	if !ok || c.Kind != ChangeLogin || c.Who != "ubuntu" || c.What != "signed in from 203.0.113.5 with a key" {
		t.Fatalf("%+v", c)
	}
	if c, _ := ParseLogin(at, "Accepted password for root from 198.51.100.7 port 1 ssh2"); c.What != "signed in from 198.51.100.7 with a password" {
		t.Errorf("%+v", c)
	}
	if _, ok := ParseLogin(at, "Failed password for root from 1.2.3.4 port 22 ssh2"); ok {
		t.Error("a failed attempt is not a sign-in")
	}
}

const aptLog = `Start-Date: 2026-03-01  06:25:01
Commandline: /usr/bin/unattended-upgrade
Upgrade: libssl3:amd64 (3.0.2-0ubuntu1.17, 3.0.2-0ubuntu1.18), openssl:amd64 (3.0.2-0ubuntu1.17, 3.0.2-0ubuntu1.18)
End-Date: 2026-03-01  06:25:05

Start-Date: 2026-03-02  14:10:00
Commandline: apt-get install -y htop nginx-extras jq curl
Requested-By: ubuntu (1000)
Install: htop:amd64 (3.0.5-7build2), nginx-extras:amd64 (1.18.0-6ubuntu14, automatic), jq:amd64 (1.6-2), curl:amd64 (7.81.0-1)
End-Date: 2026-03-02  14:10:09

Start-Date: 2026-03-02  15:00:00
Commandline: apt-get update
End-Date: 2026-03-02  15:00:02
`

func TestParseAptHistory(t *testing.T) {
	got := ParseAptHistory(aptLog, time.UTC)
	if len(got) != 2 {
		t.Fatalf("two blocks changed something, got %d: %+v", len(got), got)
	}
	if got[0].Who != "automatic updates" || got[0].What != "upgraded 2 packages: libssl3, openssl" || !strings.Contains(got[0].Detail, "upgraded: libssl3, openssl") {
		t.Errorf("%+v", got[0])
	}
	if got[1].Who != "ubuntu" || got[1].What != "installed 4 packages: htop, nginx-extras, jq and 1 more" || !got[1].At.Equal(time.Date(2026, 3, 2, 14, 10, 0, 0, time.UTC)) {
		t.Errorf("%+v", got[1])
	}
}

func TestParseWindowsChange(t *testing.T) {
	cases := map[string]string{
		"provider=Service Control Manager id=7045 level=information Tomcat9 C:\\tomcat\\bin\\tomcat9.exe user mode service auto start":                                                                         ChangeService,
		"provider=USER32 id=1074 level=information The process C:\\Windows\\system32\\shutdown.exe (WIN-1) has initiated the restart of computer WIN-1 on behalf of user CORP\\admin for the following reason": ChangeReboot,
		"provider=MsiInstaller id=11707 level=information Product: Node.js -- Installation completed successfully.":                                                                                            ChangePackage,
		"provider=Microsoft-Windows-WindowsUpdateClient id=19 level=information Security Update for Windows (KB5034441)":                                                                                       ChangePackage,
	}
	for line, kind := range cases {
		c, ok := ParseWindowsChange(at, line)
		if !ok || c.Kind != kind || c.Source != "eventlog" {
			t.Errorf("%s: %+v", line, c)
		}
	}
	if c, _ := ParseWindowsChange(at, "provider=USER32 id=1074 level=information ... on behalf of user CORP\\admin for the following reason"); c.Who != "CORP\\admin" {
		t.Errorf("who restarted it: %+v", c)
	}
	for _, line := range []string{
		"provider=Service Control Manager id=7036 level=information The Windows Update service entered the running state.",
		"provider=Application Error id=1000 level=error Faulting application name: w3wp.exe",
	} {
		if _, ok := ParseWindowsChange(at, line); ok {
			t.Errorf("not a change: %s", line)
		}
	}
}

func TestMaskSecrets(t *testing.T) {
	cases := map[string]string{
		"mysql -uroot -pS3cret! -e 'select 1'":            "mysql -uroot -p**** -e 'select 1'",
		"curl --password hunter2 https://x":               "curl --password **** https://x",
		"env DB_PASSWORD=abc API_TOKEN=xyz node app.js":   "env DB_PASSWORD=**** API_TOKEN=**** node app.js",
		"psql postgres://app:s3cret@db:5432/x":            "psql postgres://app:****@db:5432/x",
		"mysql -e \"create user x identified by 'p@ss'\"": "mysql -e \"create user x identified by ****\"",
		"systemctl restart nginx":                         "systemctl restart nginx",
	}
	for in, want := range cases {
		if got := MaskSecrets(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
}
