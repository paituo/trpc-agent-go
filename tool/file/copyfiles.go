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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// copyItem represents a single source-destination pair for copy operations.
type copyItem struct {
	// Source is the source path relative to base_directory.
	Source string `json:"source" jsonschema:"description=Source path relative to base_directory"`
	// Destination is the destination path relative to base_directory.
	Destination string `json:"destination" jsonschema:"description=Destination path relative to base_directory"`
}

// copyFilesRequest represents the input for the copy files operation.
type copyFilesRequest struct {
	// Items is the list of source-destination pairs to copy.
	Items []copyItem `json:"items" jsonschema:"description=List of source-destination pairs to copy"`
	// Overwrite controls whether existing destination files are replaced.
	Overwrite bool `json:"overwrite" jsonschema:"description=Whether to replace existing files at the destination"`
}

// copyFilesResponse represents the output from the copy files operation.
type copyFilesResponse struct {
	BaseDirectory string       `json:"base_directory"`
	Results       []itemResult `json:"results"`
	Message       string       `json:"message"`
}

// copyFiles performs the copy files operation.
func (f *fileToolSet) copyFiles(
	_ context.Context,
	req *copyFilesRequest,
) (*copyFilesResponse, error) {
	rsp := &copyFilesResponse{
		BaseDirectory: f.baseDir,
	}
	if len(req.Items) == 0 {
		rsp.Message = "Error: items cannot be empty"
		return rsp, fmt.Errorf("items cannot be empty")
	}
	var results []itemResult
	var successCount int
	for _, item := range req.Items {
		result := f.copySingleItem(item, req.Overwrite)
		results = append(results, result)
		if result.Success {
			successCount++
		}
	}
	rsp.Results = results
	rsp.Message = fmt.Sprintf(
		"Successfully copied %d of %d items",
		successCount,
		len(req.Items),
	)
	return rsp, nil
}

func (f *fileToolSet) copySingleItem(item copyItem, overwrite bool) itemResult {
	result := itemResult{
		Source:      item.Source,
		Destination: item.Destination,
	}
	// Reject file refs for source.
	ref, err := fileref.Parse(item.Source)
	if err != nil {
		result.Error = fmt.Sprintf("Error: %v", err)
		return result
	}
	if ref.Scheme != "" {
		result.Error = fmt.Sprintf(
			"Error: copy_files does not support %s:// refs",
			ref.Scheme,
		)
		return result
	}
	// Reject file refs for destination.
	dstRef, err := fileref.Parse(item.Destination)
	if err != nil {
		result.Error = fmt.Sprintf("Error: %v", err)
		return result
	}
	if dstRef.Scheme != "" {
		result.Error = fmt.Sprintf(
			"Error: copy_files does not support %s:// refs for destination",
			dstRef.Scheme,
		)
		return result
	}
	// Resolve and validate paths.
	srcPath, err := f.resolvePath(item.Source)
	if err != nil {
		result.Error = fmt.Sprintf("Error: %v", err)
		return result
	}
	dstPath, err := f.resolvePath(item.Destination)
	if err != nil {
		result.Error = fmt.Sprintf("Error: %v", err)
		return result
	}
	// Check source exists.
	srcStat, err := os.Stat(srcPath)
	if err != nil {
		result.Error = fmt.Sprintf("Error: source not found: %v", err)
		return result
	}
	// Same path check.
	if srcPath == dstPath {
		result.Success = true
		return result
	}
	// Check if destination is inside source directory.
	if srcStat.IsDir() {
		rel, err := filepath.Rel(srcPath, dstPath)
		if err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			result.Error = "Error: destination is inside source directory"
			return result
		}
	}
	// Handle existing destination.
	dstStat, err := os.Stat(dstPath)
	if err == nil {
		if !overwrite {
			result.Error = fmt.Sprintf(
				"Error: destination already exists: %s",
				item.Destination,
			)
			return result
		}
		if dstStat.IsDir() {
			if err := os.RemoveAll(dstPath); err != nil {
				result.Error = fmt.Sprintf(
					"Error: cannot remove existing destination: %v",
					err,
				)
				return result
			}
		} else {
			if err := os.Remove(dstPath); err != nil {
				result.Error = fmt.Sprintf(
					"Error: cannot remove existing destination: %v",
					err,
				)
				return result
			}
		}
	}
	// Create parent directories for destination.
	parentDir := filepath.Dir(dstPath)
	if err := os.MkdirAll(parentDir, f.createDirMode); err != nil {
		result.Error = fmt.Sprintf(
			"Error: cannot create directory: %v",
			err,
		)
		return result
	}
	// Copy the file or directory.
	if srcStat.IsDir() {
		if err := copyDirectory(srcPath, dstPath); err != nil {
			result.Error = fmt.Sprintf(
				"Error: cannot copy directory: %v",
				err,
			)
			return result
		}
	} else {
		if err := copyFile(srcPath, dstPath); err != nil {
			result.Error = fmt.Sprintf("Error: cannot copy file: %v", err)
			return result
		}
	}
	result.Success = true
	return result
}

// copyDirectory recursively copies a directory from src to dst.
func copyDirectory(src, dst string) error {
	srcStat, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, srcStat.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDirectory(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyFile copies a single file from src to dst, preserving permissions.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcStat, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(
		dst,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		srcStat.Mode(),
	)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	return nil
}

// copyFilesTool returns a callable tool for copying files.
func (f *fileToolSet) copyFilesTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.copyFiles,
		function.WithName("copy_files"),
		function.WithDescription(
			"Copy files and directories under base_directory. "+
				"Each item specifies a source and destination path. "+
				"Directories are copied recursively. "+
				"Supports copying multiple items in one call.",
		),
	)
}
