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
)

func TestFileTool_CopyFiles_SingleFile(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Create source file.
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "a.txt"),
		[]byte("hello"),
		0644,
	))
	// Copy.
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "a.txt", Destination: "b.txt"},
		},
	}
	rsp, err := fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.Len(t, rsp.Results, 1)
	assert.True(t, rsp.Results[0].Success)
	// Verify source still exists and destination has content.
	content, err := os.ReadFile(filepath.Join(tempDir, "a.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "hello", string(content))
	content, err = os.ReadFile(filepath.Join(tempDir, "b.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

func TestFileTool_CopyFiles_Directory(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Create source directory with nested files.
	assert.NoError(t, os.MkdirAll(filepath.Join(tempDir, "srcdir", "sub"), 0755))
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "srcdir", "inner.txt"),
		[]byte("inner"),
		0644,
	))
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "srcdir", "sub", "deep.txt"),
		[]byte("deep"),
		0644,
	))
	// Copy directory.
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "srcdir", Destination: "dstdir"},
		},
	}
	rsp, err := fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, rsp.Results[0].Success)
	// Verify source still exists.
	assert.DirExists(t, filepath.Join(tempDir, "srcdir"))
	// Verify destination has all files.
	content, err := os.ReadFile(
		filepath.Join(tempDir, "dstdir", "inner.txt"),
	)
	assert.NoError(t, err)
	assert.Equal(t, "inner", string(content))
	content, err = os.ReadFile(
		filepath.Join(tempDir, "dstdir", "sub", "deep.txt"),
	)
	assert.NoError(t, err)
	assert.Equal(t, "deep", string(content))
}

func TestFileTool_CopyFiles_MultipleItems(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Create source files.
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "a.txt"),
		[]byte("aaa"),
		0644,
	))
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "b.txt"),
		[]byte("bbb"),
		0644,
	))
	// Copy multiple.
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "a.txt", Destination: "c.txt"},
			{Source: "b.txt", Destination: "d.txt"},
		},
	}
	rsp, err := fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.Len(t, rsp.Results, 2)
	assert.True(t, rsp.Results[0].Success)
	assert.True(t, rsp.Results[1].Success)
	assert.Contains(t, rsp.Message, "Successfully copied 2 of 2")
}

func TestFileTool_CopyFiles_Overwrite(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Create source and destination files.
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "src.txt"),
		[]byte("source"),
		0644,
	))
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "dst.txt"),
		[]byte("dest"),
		0644,
	))
	// Copy without overwrite.
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "src.txt", Destination: "dst.txt"},
		},
	}
	rsp, err := fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
	assert.Contains(t, rsp.Results[0].Error, "already exists")
	// Copy with overwrite.
	req.Overwrite = true
	rsp, err = fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, rsp.Results[0].Success)
	content, err := os.ReadFile(filepath.Join(tempDir, "dst.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "source", string(content))
}

func TestFileTool_CopyFiles_CreateParentDir(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Create source file.
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "a.txt"),
		[]byte("hello"),
		0644,
	))
	// Copy to a non-existent subdirectory.
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "a.txt", Destination: "sub/dir/b.txt"},
		},
	}
	rsp, err := fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, rsp.Results[0].Success)
	content, err := os.ReadFile(filepath.Join(tempDir, "sub", "dir", "b.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

func TestFileTool_CopyFiles_SourceNotFound(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "nonexistent.txt", Destination: "b.txt"},
		},
	}
	rsp, err := fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
	assert.Contains(t, rsp.Results[0].Error, "source not found")
}

func TestFileTool_CopyFiles_SamePath(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "a.txt"),
		[]byte("hello"),
		0644,
	))
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "a.txt", Destination: "a.txt"},
		},
	}
	rsp, err := fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, rsp.Results[0].Success)
}

func TestFileTool_CopyFiles_DestinationInsideSource(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	assert.NoError(t, os.MkdirAll(filepath.Join(tempDir, "srcdir"), 0755))
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "srcdir", Destination: "srcdir/sub"},
		},
	}
	rsp, err := fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
	assert.Contains(t, rsp.Results[0].Error, "inside source")
}

func TestFileTool_CopyFiles_EmptyItems(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	req := &copyFilesRequest{Items: []copyItem{}}
	_, err := fts.copyFiles(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_CopyFiles_DirTraversal(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "../a.txt", Destination: "b.txt"},
		},
	}
	rsp, err := fileToolSet.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
}

func TestFileTool_CopyFiles_RejectsFileRef(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "workspace://out/a.txt", Destination: "b.txt"},
		},
	}
	rsp, err := fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
	assert.Contains(t, rsp.Results[0].Error, "does not support")
}

func TestFileTool_CopyFiles_PreservePermissions(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Create source file.
	srcPath := filepath.Join(tempDir, "perm.txt")
	assert.NoError(t, os.WriteFile(srcPath, []byte("data"), 0600))
	// Copy.
	req := &copyFilesRequest{
		Items: []copyItem{
			{Source: "perm.txt", Destination: "perm_copy.txt"},
		},
	}
	rsp, err := fts.copyFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, rsp.Results[0].Success)
	// Verify content is copied correctly.
	content, err := os.ReadFile(filepath.Join(tempDir, "perm_copy.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "data", string(content))
}
