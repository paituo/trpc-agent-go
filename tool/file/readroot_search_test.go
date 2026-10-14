//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// list_file over read-only roots
// ---------------------------------------------------------------------------

// TestListFile_ReadOnlyRoot_Allowed ensures list_file can enumerate a directory
// under a configured read_only_dir (extra read root), which was previously
// rejected by resolvePathCtx as "outside base directory".
func TestListFile_ReadOnlyRoot_Allowed(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(extra, "a.txt"), []byte("a"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(extra, "b.txt"), []byte("b"), 0644))

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(extra))
	require.NoError(t, err)
	fts := set.(*fileToolSet)

	rsp, err := fts.listFile(context.Background(), &listFileRequest{Path: extra})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.txt", "b.txt"}, fileInfoNames(rsp.Files))
	// BaseDirectory stays the session/static base, not the extra root.
	assert.Equal(t, base, rsp.BaseDirectory)
}

// TestListFile_ReadOnlyRoot_Recursive ensures recursive listing also works
// under an extra read root.
func TestListFile_ReadOnlyRoot_Recursive(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(extra, "sub/deep"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(extra, "root.txt"), []byte("r"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(extra, "sub", "a.txt"), []byte("a"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(extra, "sub", "deep", "b.txt"), []byte("b"), 0644))

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(extra))
	require.NoError(t, err)
	fts := set.(*fileToolSet)

	rsp, err := fts.listFile(context.Background(), &listFileRequest{
		Path:      extra,
		Recursive: true,
	})
	require.NoError(t, err)
	assert.ElementsMatch(t,
		[]string{"root.txt", "sub/a.txt", "sub/deep/b.txt"},
		fileInfoNames(rsp.Files))
	assert.ElementsMatch(t, []string{"sub", "sub/deep"}, rsp.Folders)
}

// TestListFile_OutsideReadOnlyRoot_Rejected ensures absolute paths that are
// neither under base_dir nor under a read_only_dir are still rejected.
func TestListFile_OutsideReadOnlyRoot_Rejected(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x.txt"), []byte("x"), 0644))

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(extra))
	require.NoError(t, err)
	fts := set.(*fileToolSet)

	rsp, err := fts.listFile(context.Background(), &listFileRequest{Path: outside})
	require.Error(t, err)
	assert.Contains(t, rsp.Message, "outside base_directory")
}

// TestListFile_ReadOnlyRoot_RelativeStaysInBase ensures relative paths still
// resolve under the (session) base directory, not the extra read root.
func TestListFile_ReadOnlyRoot_RelativeStaysInBase(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(base, "inbase.txt"), []byte("in"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(extra, "inextra.txt"), []byte("ex"), 0644))

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(extra))
	require.NoError(t, err)
	fts := set.(*fileToolSet)

	// empty path => base directory
	rsp, err := fts.listFile(context.Background(), &listFileRequest{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"inbase.txt"}, fileInfoNames(rsp.Files))
	assert.Empty(t, rsp.Folders)
}

// ---------------------------------------------------------------------------
// search_file over read-only roots
// ---------------------------------------------------------------------------

// TestSearchFile_ReadOnlyRoot_Allowed ensures search_file can match files
// under a configured read_only_dir.
func TestSearchFile_ReadOnlyRoot_Allowed(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(extra, "report_final.xlsx"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(extra, "draft.xlsx"), []byte("x"), 0644))

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(extra))
	require.NoError(t, err)
	fts := set.(*fileToolSet)

	rsp, err := fts.searchFile(context.Background(), &searchFileRequest{
		Path:    extra,
		Pattern: "*.xlsx",
	})
	require.NoError(t, err)
	assert.ElementsMatch(t,
		[]string{
			filepath.ToSlash(filepath.Join(extra, "report_final.xlsx")),
			filepath.ToSlash(filepath.Join(extra, "draft.xlsx")),
		},
		rsp.Files,
	)
}

// TestSearchFile_ReadOnlyRoot_OutsideRejected ensures search_file still rejects
// absolute paths outside base_dir and read_only_dirs.
func TestSearchFile_ReadOnlyRoot_OutsideRejected(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x.txt"), []byte("x"), 0644))

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(extra))
	require.NoError(t, err)
	fts := set.(*fileToolSet)

	rsp, err := fts.searchFile(context.Background(), &searchFileRequest{
		Path:    outside,
		Pattern: "*",
	})
	require.Error(t, err)
	assert.Contains(t, rsp.Message, "outside base_directory")
}

// ---------------------------------------------------------------------------
// search_content over read-only roots
// ---------------------------------------------------------------------------

// TestSearchContent_ReadOnlyRoot_Allowed ensures search_content can search
// inside a directory under a configured read_only_dir.
func TestSearchContent_ReadOnlyRoot_Allowed(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(extra, "c.txt"), []byte("hello fox"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(extra, "d.txt"), []byte("no match here"), 0644))

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(extra))
	require.NoError(t, err)
	fts := set.(*fileToolSet)

	rsp, err := fts.searchContent(context.Background(), &searchContentRequest{
		Path:           extra,
		FilePattern:    "*.txt",
		ContentPattern: "fox",
	})
	require.NoError(t, err)
	require.Len(t, rsp.FileMatches, 1)
	assert.Equal(t, filepath.ToSlash(filepath.Join(extra, "c.txt")), rsp.FileMatches[0].FilePath)
}

// TestSearchContent_ReadOnlyRoot_OutsideRejected ensures search_content still
// rejects absolute paths outside base_dir and read_only_dirs.
func TestSearchContent_ReadOnlyRoot_OutsideRejected(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x.txt"), []byte("fox"), 0644))

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(extra))
	require.NoError(t, err)
	fts := set.(*fileToolSet)

	rsp, err := fts.searchContent(context.Background(), &searchContentRequest{
		Path:           outside,
		FilePattern:    "*.txt",
		ContentPattern: "fox",
	})
	require.Error(t, err)
	assert.Contains(t, rsp.Message, "outside base_directory")
}

// ---------------------------------------------------------------------------
// Write tools remain locked to base_dir
// ---------------------------------------------------------------------------

// TestWriteTools_ReadOnlyRoot_StillRejected ensures save_file / replace_content
// still refuse paths under a read_only_dir (write boundary unchanged).
func TestWriteTools_ReadOnlyRoot_StillRejected(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(extra))
	require.NoError(t, err)
	fts := set.(*fileToolSet)
	ctx := context.Background()

	// save_file
	rsp, err := fts.saveFile(ctx, &saveFileRequest{
		FileName: filepath.Join(extra, "new.txt"),
		Contents: "data",
	})
	require.Error(t, err)
	assert.Contains(t, rsp.Message, "outside base directory")

	// replace_content
	require.NoError(t, os.WriteFile(filepath.Join(extra, "existing.txt"), []byte("old"), 0644))
	rrsp, err := fts.replaceContent(ctx, &replaceContentRequest{
		FileName:  filepath.Join(extra, "existing.txt"),
		OldString: "old",
		NewString: "new",
	})
	require.Error(t, err)
	assert.Contains(t, rrsp.Message, "outside base directory")
}

// ---------------------------------------------------------------------------
// Symlink must not escape the read-only root during recursion
// ---------------------------------------------------------------------------

// TestListFile_ReadOnlyRoot_SymlinkEscapeRejected ensures recursive listing
// under an extra read root does not leak files reachable only through a
// symlink that points outside the read-only root.
func TestListFile_ReadOnlyRoot_SymlinkEscapeRejected(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()
	outside := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644))
	if err := os.Symlink(outside, filepath.Join(extra, "link")); err != nil {
		t.Skip("symlink not supported on this platform")
	}

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(extra))
	require.NoError(t, err)
	fts := set.(*fileToolSet)

	// filepath.WalkDir does not follow directory symlinks, so the target of the
	// symlink (outside root) must not be enumerated, even though it is readable.
	rsp, err := fts.listFile(context.Background(), &listFileRequest{
		Path:      extra,
		Recursive: true,
	})
	require.NoError(t, err)
	names := fileInfoNames(rsp.Files)
	for _, n := range names {
		assert.NotContains(t, filepath.FromSlash(n), "secret")
	}
}
