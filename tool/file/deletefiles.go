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
	"os"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// deleteFilesRequest represents the input for the delete files operation.
type deleteFilesRequest struct {
	// Paths is the list of paths relative to base_directory to delete.
	Paths []string `json:"paths" jsonschema:"description=List of paths relative to base_directory to delete"`
}

// deleteResult represents the result of a single delete operation.
type deleteResult struct {
	Path    string `json:"path"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// deleteFilesResponse represents the output from the delete files operation.
type deleteFilesResponse struct {
	BaseDirectory string         `json:"base_directory"`
	Results       []deleteResult `json:"results"`
	Message       string         `json:"message"`
}

// deleteFiles performs the delete files operation.
func (f *fileToolSet) deleteFiles(
	_ context.Context,
	req *deleteFilesRequest,
) (*deleteFilesResponse, error) {
	rsp := &deleteFilesResponse{
		BaseDirectory: f.baseDir,
	}
	if len(req.Paths) == 0 {
		rsp.Message = "Error: paths cannot be empty"
		return rsp, fmt.Errorf("paths cannot be empty")
	}
	var results []deleteResult
	var successCount int
	for _, path := range req.Paths {
		result := f.deleteSinglePath(path)
		results = append(results, result)
		if result.Success {
			successCount++
		}
	}
	rsp.Results = results
	rsp.Message = fmt.Sprintf(
		"Successfully deleted %d of %d items",
		successCount,
		len(req.Paths),
	)
	return rsp, nil
}

func (f *fileToolSet) deleteSinglePath(path string) deleteResult {
	result := deleteResult{Path: path}
	// Reject file refs.
	ref, err := fileref.Parse(path)
	if err != nil {
		result.Error = fmt.Sprintf("Error: %v", err)
		return result
	}
	if ref.Scheme != "" {
		result.Error = fmt.Sprintf(
			"Error: delete_files does not support %s:// refs",
			ref.Scheme,
		)
		return result
	}
	// Resolve and validate path.
	resolvedPath, err := f.resolvePath(path)
	if err != nil {
		result.Error = fmt.Sprintf("Error: %v", err)
		return result
	}
	// Check path exists.
	stat, err := os.Stat(resolvedPath)
	if err != nil {
		result.Error = fmt.Sprintf("Error: path not found: %v", err)
		return result
	}
	// Delete.
	if stat.IsDir() {
		if err := os.RemoveAll(resolvedPath); err != nil {
			result.Error = fmt.Sprintf(
				"Error: cannot delete directory: %v",
				err,
			)
			return result
		}
	} else {
		if err := os.Remove(resolvedPath); err != nil {
			result.Error = fmt.Sprintf("Error: cannot delete file: %v", err)
			return result
		}
	}
	result.Success = true
	return result
}

// deleteFilesTool returns a callable tool for deleting files.
func (f *fileToolSet) deleteFilesTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.deleteFiles,
		function.WithName("delete_files"),
		function.WithDescription(
			"Delete files and directories under base_directory. "+
				"Directories are deleted recursively. "+
				"Supports deleting multiple items in one call.",
		),
	)
}
