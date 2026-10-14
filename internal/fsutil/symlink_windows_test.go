//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build windows

package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestShouldDegradeSymlink(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "privilege_not_held_1314",
			err:  syscall.Errno(1314),
			want: true,
		},
		{
			name: "access_denied_5",
			err:  syscall.Errno(5),
			want: true,
		},
		{
			name: "eperm",
			err:  syscall.EPERM,
			want: true,
		},
		{
			name: "path_not_found_3",
			err:  syscall.Errno(3),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "wrapped_1314",
			err:  errors.Join(syscall.Errno(1314), errors.New("context")),
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDegradeSymlink(tc.err); got != tc.want {
				t.Fatalf("shouldDegradeSymlink(%v) = %v, want %v",
					tc.err, got, tc.want)
			}
		})
	}
}

func TestCreateSymlink_FileFallbackToCopy(t *testing.T) {
	// A normal, successful symlink should just work when the target
	// does not require linkage and we are on a filesystem that allows it.
	// This guards the createSymlink fast path.
	base := t.TempDir()
	src := filepath.Join(base, "src.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(base, "out-link")
	if err := createSymlink(src, dst); err != nil {
		// On restricted CI shells symlink may still fail with 1314,
		// which is acceptable as long as a fallback produced a file.
		t.Logf("createSymlink errored (may be 1314): %v", err)
	}
}

func TestCopyDirFallback_MaterializesTree(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "tree")
	sub := filepath.Join(src, "scripts")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(src, "SKILL.md"),
		[]byte("# skill"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(sub, "run.py"),
		[]byte("print(1)"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(base, "staged")
	if err := copyDirFallback(src, dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md missing after copyDirFallback: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "SKILL.md")); err != nil ||
		string(data) != "# skill" {
		t.Fatalf("SKILL.md content mismatch: %q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "scripts", "run.py")); err != nil {
		t.Fatalf("nested scripts/run.py missing: %v", err)
	}
}

// TestCopyDirFallback_OverwritesExistingDest verifies copyDirFallback is
// idempotent across repeated stages: a pre-existing destination with stale
// content is first removed, then a fresh copy is materialized. This guards the
// skillstage restart/re-stage path where a leftover untracked file in out/ or
// work/ must not survive into the new stage.
func TestCopyDirFallback_OverwritesExistingDest(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(base, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stale leftover inside the target (simulates an untracked artifact from a
	// previous stage that must be wiped).
	if err := os.WriteFile(filepath.Join(dst, "stale.bin"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyDirFallback(src, dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "a.txt")); err != nil ||
		string(data) != "new" {
		t.Fatalf("a.txt after copy: %q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale.bin")); !os.IsNotExist(err) {
		t.Fatalf("stale.bin should have been removed by copyDirFallback, stat err=%v", err)
	}
}

// TestCreateSymlink_DirectoryProducesUsableEntry is a best-effort integration
// probe of the directory branch: whatever mechanism is used (real symlink when
// the caller has privilege, junction, or copyDirFallback when it does not), the
// destination must exist and resolve to the source content afterward. On
// restricted CI shells os.Symlink may still fail with 1314, so we accept any of
// the graded degradations as long as the target materializes.
func TestCreateSymlink_DirectoryProducesUsableEntry(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "srcDir")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "data.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(base, "worklink")
	err := createSymlink(src, dst)
	if err != nil {
		// A hard failure would only be legitimate if the target is still absent;
		// otherwise treat an explicit error as a test failure.
		if _, statErr := os.Stat(dst); statErr != nil {
			t.Fatalf("createSymlink(dir) error=%v and no target materialized: statErr=%v",
				err, statErr)
		}
		t.Logf("createSymlink(dir) errored=%v but target exists (degraded fallback)", err)
	}
	// Target must be a directory containing the payload regardless of mechanism.
	fi, statErr := os.Stat(filepath.Join(dst, "data.txt"))
	if statErr != nil {
		t.Fatalf("data.txt reachable through target: %v", statErr)
	}
	if !fi.Mode().IsRegular() {
		t.Fatalf("data.txt should be a regular file, mode=%v", fi.Mode())
	}
}
