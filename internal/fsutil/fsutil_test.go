// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicReplaces(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	for _, body := range []string{"one", "two"} {
		if err := WriteAtomic(p, []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("got %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), ".pulse-*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestCopyAtomic(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "sub", "dst")
	if err := os.WriteFile(src, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if copied, err := CopyAtomic(src, dst, 0o755); err != nil || !copied {
		t.Fatalf("CopyAtomic = %v, %v", copied, err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "binary" {
		t.Fatalf("got %q", b)
	}
	if copied, err := CopyAtomic(dst, dst, 0o755); err != nil || copied {
		t.Fatalf("same file should be skipped: %v, %v", copied, err)
	}
}
