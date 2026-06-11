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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
)

func fileInfoNames(files []fileInfo) []string {
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	return names
}

func TestFileTool_listFile(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create some test files.
	testFiles := []string{"file1.txt", "file2.go", "README.md"}
	for _, fileName := range testFiles {
		filePath := filepath.Join(tempDir, fileName)
		err := os.WriteFile(filePath, []byte("test content"), 0644)
		assert.NoError(t, err)
	}
	// Test listing files in base directory.
	req := &listFileRequest{}
	rsp, err := fileToolSet.listFile(context.Background(), req)
	assert.NoError(t, err)
	// Check that the response contains the expected base directory.
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, "", rsp.Path)
	// Check that the number of files matches.
	assert.Equal(t, len(testFiles), len(rsp.Files))
	// Check that all test files are in the response.
	assert.ElementsMatch(t, testFiles, fileInfoNames(rsp.Files))
	// Check that there are no folders in root.
	assert.Equal(t, 0, len(rsp.Folders))
}

func TestFileTool_listFile_Subdirectory(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create a subdirectory with files.
	subDir := filepath.Join(tempDir, "subdir")
	err := os.MkdirAll(subDir, 0755)
	assert.NoError(t, err)
	// Create some test files in subdirectory.
	testFiles := []string{"file1.txt", "file2.go", "README.md"}
	for _, fileName := range testFiles {
		filePath := filepath.Join(subDir, fileName)
		err := os.WriteFile(filePath, []byte("test content"), 0644)
		assert.NoError(t, err)
	}
	// Test listing files in subdirectory.
	req := &listFileRequest{Path: "subdir"}
	rsp, err := fileToolSet.listFile(context.Background(), req)
	assert.NoError(t, err)
	// Check that the response contains the expected base directory.
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, "subdir", rsp.Path)
	// Check that the number of files matches.
	assert.Equal(t, len(testFiles), len(rsp.Files))
	// Check that all test files are in the response.
	assert.ElementsMatch(t, testFiles, fileInfoNames(rsp.Files))
	// Check that there are no folders in subdirectory.
	assert.Equal(t, 0, len(rsp.Folders))
}

func TestFileTool_listFile_WithFolders(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create some test files.
	testFiles := []string{"file1.txt", "file2.go", "README.md"}
	for _, fileName := range testFiles {
		filePath := filepath.Join(tempDir, fileName)
		err := os.WriteFile(filePath, []byte("test content"), 0644)
		assert.NoError(t, err)
	}
	// Create some test folders.
	testFolders := []string{"docs", "src", "tests"}
	for _, folderName := range testFolders {
		folderPath := filepath.Join(tempDir, folderName)
		err := os.MkdirAll(folderPath, 0755)
		assert.NoError(t, err)
	}
	// Test listing files and folders in base directory.
	req := &listFileRequest{}
	rsp, err := fileToolSet.listFile(context.Background(), req)
	assert.NoError(t, err)
	// Check that the response contains the expected base directory.
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, "", rsp.Path)
	// Check that the number of files matches.
	assert.Equal(t, len(testFiles), len(rsp.Files))
	// Check that all test files are in the response.
	assert.ElementsMatch(t, testFiles, fileInfoNames(rsp.Files))
	// Check that the number of folders matches.
	assert.Equal(t, len(testFolders), len(rsp.Folders))
	// Check that all test folders are in the response.
	assert.ElementsMatch(t, testFolders, rsp.Folders)
}

func TestFileTool_listFile_DirTraversal(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := set.(*fileToolSet)
	assert.True(t, ok)
	// Test listing files in subdirectory.
	req := &listFileRequest{Path: "../"}
	_, err = fileToolSet.listFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_listFile_NotExist(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := set.(*fileToolSet)
	assert.True(t, ok)
	// Test listing files in subdirectory.
	req := &listFileRequest{Path: "notexist"}
	_, err = fileToolSet.listFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_listFile_IsFile(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := set.(*fileToolSet)
	assert.True(t, ok)
	// Create a file.
	file := filepath.Join(tempDir, "file.txt")
	err = os.WriteFile(file, []byte("test content"), 0644)
	assert.NoError(t, err)
	// Test listing files in subdirectory.
	req := &listFileRequest{Path: "file.txt"}
	_, err = fileToolSet.listFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_listFile_WorkspaceRef(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     ".",
			Content:  "ignored",
			MIMEType: "text/plain",
		},
		{
			Name:     "root.txt",
			Content:  "root",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/a.txt",
			Content:  "a",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/sub/b.txt",
			Content:  "b",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fts.listFile(ctx, &listFileRequest{
		Path: "workspace://",
	})
	assert.NoError(t, err)
	assert.Equal(t, "workspace://", rsp.Path)
	assert.ElementsMatch(t, []string{"workspace://root.txt"}, fileInfoNames(rsp.Files))
	assert.ElementsMatch(t, []string{"workspace://out"}, rsp.Folders)

	rsp, err = fts.listFile(ctx, &listFileRequest{
		Path: "workspace://out",
	})
	assert.NoError(t, err)
	assert.Equal(t, "workspace://out", rsp.Path)
	assert.ElementsMatch(t, []string{"workspace://out/a.txt"}, fileInfoNames(rsp.Files))
	assert.ElementsMatch(t, []string{"workspace://out/sub"}, rsp.Folders)
}

func TestFileTool_listFile_WorkspaceRef_Recursive(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     ".",
			Content:  "ignored",
			MIMEType: "text/plain",
		},
		{
			Name:     "root.txt",
			Content:  "root",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/a.txt",
			Content:  "a",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/sub/b.txt",
			Content:  "b",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/sub/deep/c.txt",
			Content:  "c",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fts.listFile(ctx, &listFileRequest{
		Path:      "workspace://",
		Recursive: true,
	})
	assert.NoError(t, err)
	assert.Equal(t, "workspace://", rsp.Path)
	assert.ElementsMatch(t,
		[]string{"workspace://root.txt", "workspace://out/a.txt", "workspace://out/sub/b.txt", "workspace://out/sub/deep/c.txt"},
		fileInfoNames(rsp.Files))
	assert.ElementsMatch(t, []string{"workspace://out", "workspace://out/sub", "workspace://out/sub/deep"}, rsp.Folders)
}

func TestFileTool_listFile_ArtifactUnsupported(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	_, err = fts.listFile(context.Background(), &listFileRequest{
		Path: "artifact://x.txt",
	})
	assert.Error(t, err)
}

func TestFileTool_listFile_ParseError(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	_, err = fts.listFile(context.Background(), &listFileRequest{
		Path: "unknown://x",
	})
	assert.Error(t, err)
}

func TestFileTool_listFile_WithSize(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create some test files with known content sizes.
	testFiles := map[string]string{
		"file1.txt": "hello",        // 5 bytes
		"file2.go":  "package main", // 12 bytes
		"README.md": "# Readme",     // 8 bytes
	}
	for fileName, content := range testFiles {
		filePath := filepath.Join(tempDir, fileName)
		err := os.WriteFile(filePath, []byte(content), 0644)
		assert.NoError(t, err)
	}
	// Create a subdirectory (should not appear in files).
	err := os.MkdirAll(filepath.Join(tempDir, "subdir"), 0755)
	assert.NoError(t, err)

	// Test listing files with WithSize = true.
	req := &listFileRequest{WithSize: true}
	rsp, err := fileToolSet.listFile(context.Background(), req)
	assert.NoError(t, err)
	// Check that the response contains the expected base directory.
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, "", rsp.Path)
	// Check that the number of files matches.
	assert.Equal(t, len(testFiles), len(rsp.Files))
	// Check that file sizes are included in Files.
	expectedSizes := map[string]int64{
		"file1.txt": 5,
		"file2.go":  12,
		"README.md": 8,
	}
	for _, fi := range rsp.Files {
		expectedSize, ok := expectedSizes[fi.Name]
		assert.True(t, ok, "unexpected file: %s", fi.Name)
		assert.Equal(t, expectedSize, fi.Size, "size mismatch for file: %s", fi.Name)
	}
}

func TestFileTool_listFile_Recursive(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}

	// Create nested directory structure.
	type fileEntry struct {
		path    string
		content string
	}
	entries := []fileEntry{
		{"root.txt", "root"},
		{"src/main.go", "package main"},
		{"src/lib/helper.go", "package lib"},
		{"src/lib/util.go", "package lib"},
		{"docs/readme.md", "# Readme"},
		{"docs/api/index.html", "<html></html>"},
	}
	for _, e := range entries {
		fullPath := filepath.Join(tempDir, e.path)
		err := os.MkdirAll(filepath.Dir(fullPath), 0755)
		assert.NoError(t, err)
		err = os.WriteFile(fullPath, []byte(e.content), 0644)
		assert.NoError(t, err)
	}

	// Test recursive listing.
	req := &listFileRequest{Recursive: true}
	rsp, err := fileToolSet.listFile(context.Background(), req)
	assert.NoError(t, err)

	// Check that all files are returned with relative paths.
	expectedFiles := []string{
		"root.txt",
		"src/main.go",
		"src/lib/helper.go",
		"src/lib/util.go",
		"docs/readme.md",
		"docs/api/index.html",
	}
	assert.ElementsMatch(t, expectedFiles, fileInfoNames(rsp.Files))

	// Check that all directories are returned.
	expectedFolders := []string{
		"src",
		"src/lib",
		"docs",
		"docs/api",
	}
	assert.ElementsMatch(t, expectedFolders, rsp.Folders)
}

func TestFileTool_listFile_Recursive_WithSize(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}

	// Create nested directory structure with known sizes.
	type fileEntry struct {
		path    string
		content string
	}
	entries := []fileEntry{
		{"root.txt", "hello"},              // 5 bytes
		{"src/main.go", "package main"},    // 12 bytes
		{"src/lib/util.go", "package lib"}, // 11 bytes
	}
	for _, e := range entries {
		fullPath := filepath.Join(tempDir, e.path)
		err := os.MkdirAll(filepath.Dir(fullPath), 0755)
		assert.NoError(t, err)
		err = os.WriteFile(fullPath, []byte(e.content), 0644)
		assert.NoError(t, err)
	}

	// Test recursive listing with size.
	req := &listFileRequest{Recursive: true, WithSize: true}
	rsp, err := fileToolSet.listFile(context.Background(), req)
	assert.NoError(t, err)

	expectedSizes := map[string]int64{
		"root.txt":          5,
		"src/main.go":       12,
		"src/lib/util.go":   11,
	}
	assert.Equal(t, len(expectedSizes), len(rsp.Files))
	for _, fi := range rsp.Files {
		expectedSize, ok := expectedSizes[fi.Name]
		assert.True(t, ok, "unexpected file: %s", fi.Name)
		assert.Equal(t, expectedSize, fi.Size, "size mismatch for file: %s", fi.Name)
	}
}

func TestFileTool_listFile_FallbackToWorkspaceCache(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "a",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/sub/b.txt",
			Content:  "b",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fts.listFile(ctx, &listFileRequest{Path: "out"})
	assert.NoError(t, err)
	assert.Equal(t, "workspace://out", rsp.Path)
	assert.ElementsMatch(t, []string{"workspace://out/a.txt"}, fileInfoNames(rsp.Files))
	assert.ElementsMatch(t, []string{"workspace://out/sub"}, rsp.Folders)
}