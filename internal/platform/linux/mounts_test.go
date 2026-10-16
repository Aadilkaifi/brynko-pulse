// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package linux

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseMountsKeepsDataFilesystemsOnce(t *testing.T) {
	in := `/dev/root / ext4 rw,relatime 0 0
proc /proc proc rw 0 0
tmpfs /run tmpfs rw 0 0
/dev/nvme0n1p15 /boot/efi vfat rw 0 0
/dev/nvme1n1 /data xfs rw 0 0
overlay /var/lib/docker/overlay2/abc/merged overlay rw 0 0
/dev/sdb1 /mnt/my\040disk ext4 rw 0 0
/dev/root / ext4 rw 0 0
/dev/loop0 /snap/core/1 squashfs ro 0 0
`
	got := parseMounts(strings.NewReader(in))
	// /dev/root names nothing; a real device is shown without /dev/.
	want := []mountPoint{{"/", "ext4", ""}, {"/data", "xfs", "nvme1n1"}, {"/mnt/my disk", "ext4", "sdb1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUnescapeMount(t *testing.T) {
	for in, want := range map[string]string{`/a\040b`: "/a b", `/a\011b`: "/a\tb", `/a\134b`: `/a\b`, "/plain": "/plain", `/end\04`: `/end\04`} {
		if got := unescapeMount(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
