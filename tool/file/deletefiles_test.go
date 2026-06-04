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

func TestFileTool_DeleteFiles_SingleFile(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	// Create a file.
	filePath := filepath.Join(tempDir, "a.txt")
	assert.NoError(t, os.WriteFile(filePath, []byte("hello"), 0644))
	// Delete.
	req := &deleteFilesRequest{
		Paths: []string{"a.txt"},
	}
	rsp, err := fts.deleteFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.Len(t, rsp.Results, 1)
	assert.True(t, rsp.Results[0].Success)
	assert.NoFileExists(t, filePath)
}

func TestFileTool_DeleteFiles_Directory(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	// Create a directory with files.
	assert.NoError(t, os.MkdirAll(filepath.Join(tempDir, "dir", "sub"), 0755))
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "dir", "a.txt"),
		[]byte("hello"),
		0644,
	))
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "dir", "sub", "b.txt"),
		[]byte("world"),
		0644,
	))
	// Delete directory.
	req := &deleteFilesRequest{
		Paths: []string{"dir"},
	}
	rsp, err := fts.deleteFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, rsp.Results[0].Success)
	assert.NoDirExists(t, filepath.Join(tempDir, "dir"))
}

func TestFileTool_DeleteFiles_MultiplePaths(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	// Create files.
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
	// Delete multiple.
	req := &deleteFilesRequest{
		Paths: []string{"a.txt", "b.txt"},
	}
	rsp, err := fts.deleteFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.Len(t, rsp.Results, 2)
	assert.True(t, rsp.Results[0].Success)
	assert.True(t, rsp.Results[1].Success)
	assert.Contains(t, rsp.Message, "Successfully deleted 2 of 2")
}

func TestFileTool_DeleteFiles_PathNotFound(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	req := &deleteFilesRequest{
		Paths: []string{"nonexistent.txt"},
	}
	rsp, err := fts.deleteFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
	assert.Contains(t, rsp.Results[0].Error, "path not found")
}

func TestFileTool_DeleteFiles_EmptyPaths(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	req := &deleteFilesRequest{Paths: []string{}}
	_, err := fts.deleteFiles(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_DeleteFiles_DirTraversal(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	req := &deleteFilesRequest{
		Paths: []string{"../a.txt"},
	}
	rsp, err := fileToolSet.deleteFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
}

func TestFileTool_DeleteFiles_RejectsFileRef(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	req := &deleteFilesRequest{
		Paths: []string{"workspace://out/a.txt"},
	}
	rsp, err := fts.deleteFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, rsp.Results[0].Success)
	assert.Contains(t, rsp.Results[0].Error, "does not support")
}

func TestFileTool_DeleteFiles_PartialFailure(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	// Create one file.
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "exists.txt"),
		[]byte("data"),
		0644,
	))
	// Delete one existing and one non-existent path.
	req := &deleteFilesRequest{
		Paths: []string{"exists.txt", "nope.txt"},
	}
	rsp, err := fts.deleteFiles(context.Background(), req)
	assert.NoError(t, err)
	assert.Len(t, rsp.Results, 2)
	assert.True(t, rsp.Results[0].Success)
	assert.False(t, rsp.Results[1].Success)
	assert.Contains(t, rsp.Message, "Successfully deleted 1 of 2")
}
