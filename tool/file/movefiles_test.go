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

func TestFileTool_MoveFiles_SingleFile(t *testing.T) {
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
	// Move.
	req := &moveFilesRequest{
		Items: []moveItem{
			{Source: "a.txt", Destination: "b.txt"},
		},
	}
	rsp, err := fts.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Len(t, rsp.Results, 1)
	assert.True(t, rsp.Results[0].Success)
	// Verify source is gone and destination exists.
	assert.NoFileExists(t, filepath.Join(tempDir, "a.txt"))
	content, err := os.ReadFile(filepath.Join(tempDir, "b.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

func TestFileTool_MoveFiles_Directory(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Create source directory with a file.
	assert.NoError(t, os.MkdirAll(filepath.Join(tempDir, "srcdir"), 0755))
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "srcdir", "inner.txt"),
		[]byte("inner"),
		0644,
	))
	// Move directory.
	req := &moveFilesRequest{
		Items: []moveItem{
			{Source: "srcdir", Destination: "dstdir"},
		},
	}
	rsp, err := fts.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, rsp.Results[0].Success)
	// Verify.
	assert.NoDirExists(t, filepath.Join(tempDir, "srcdir"))
	content, err := os.ReadFile(
		filepath.Join(tempDir, "dstdir", "inner.txt"),
	)
	assert.NoError(t, err)
	assert.Equal(t, "inner", string(content))
}

func TestFileTool_MoveFiles_MultipleItems(t *testing.T) {
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
	// Move multiple.
	req := &moveFilesRequest{
		Items: []moveItem{
			{Source: "a.txt", Destination: "c.txt"},
			{Source: "b.txt", Destination: "d.txt"},
		},
	}
	rsp, err := fts.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.Len(t, rsp.Results, 2)
	assert.True(t, rsp.Results[0].Success)
	assert.True(t, rsp.Results[1].Success)
	assert.Contains(t, rsp.Message, "Successfully moved 2 of 2")
}

func TestFileTool_MoveFiles_Overwrite(t *testing.T) {
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
	// Move without overwrite.
	req := &moveFilesRequest{
		Items: []moveItem{
			{Source: "src.txt", Destination: "dst.txt"},
		},
	}
	rsp, err := fts.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
	assert.Contains(t, rsp.Results[0].Error, "already exists")
	// Move with overwrite.
	req.Overwrite = true
	rsp, err = fts.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, rsp.Results[0].Success)
	content, err := os.ReadFile(filepath.Join(tempDir, "dst.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "source", string(content))
}

func TestFileTool_MoveFiles_CreateParentDir(t *testing.T) {
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
	// Move to a non-existent subdirectory.
	req := &moveFilesRequest{
		Items: []moveItem{
			{Source: "a.txt", Destination: "sub/dir/b.txt"},
		},
	}
	rsp, err := fts.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, rsp.Results[0].Success)
	content, err := os.ReadFile(filepath.Join(tempDir, "sub", "dir", "b.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

func TestFileTool_MoveFiles_SourceNotFound(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	req := &moveFilesRequest{
		Items: []moveItem{
			{Source: "nonexistent.txt", Destination: "b.txt"},
		},
	}
	rsp, err := fts.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
	assert.Contains(t, rsp.Results[0].Error, "source not found")
}

func TestFileTool_MoveFiles_SamePath(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "a.txt"),
		[]byte("hello"),
		0644,
	))
	req := &moveFilesRequest{
		Items: []moveItem{
			{Source: "a.txt", Destination: "a.txt"},
		},
	}
	rsp, err := fts.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, rsp.Results[0].Success)
}

func TestFileTool_MoveFiles_EmptyItems(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	req := &moveFilesRequest{Items: []moveItem{}}
	_, err := fts.moveFiles(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_MoveFiles_DirTraversal(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	req := &moveFilesRequest{
		Items: []moveItem{
			{Source: "../a.txt", Destination: "b.txt"},
		},
	}
	rsp, err := fileToolSet.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
}

func TestFileTool_MoveFiles_RejectsFileRef(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	req := &moveFilesRequest{
		Items: []moveItem{
			{Source: "workspace://out/a.txt", Destination: "b.txt"},
		},
	}
	rsp, err := fts.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
	assert.Contains(t, rsp.Results[0].Error, "does not support")

	// Also test destination ref.
	req2 := &moveFilesRequest{
		Items: []moveItem{
			{Source: "a.txt", Destination: "workspace://out/b.txt"},
		},
	}
	rsp2, err := fts.moveFiles(context.Background(), req2)
	assert.NoError(t, err)
	assert.False(t, rsp2.Results[0].Success)
}

func TestFileTool_MoveFiles_PartialFailure(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Create one source file.
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "exists.txt"),
		[]byte("data"),
		0644,
	))
	// Move one existing and one non-existent file.
	req := &moveFilesRequest{
		Items: []moveItem{
			{Source: "exists.txt", Destination: "moved.txt"},
			{Source: "nope.txt", Destination: "nope2.txt"},
		},
	}
	rsp, err := fts.moveFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.Len(t, rsp.Results, 2)
	assert.True(t, rsp.Results[0].Success)
	assert.False(t, rsp.Results[1].Success)
	assert.Contains(t, rsp.Message, "Successfully moved 1 of 2")
}
