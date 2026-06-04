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
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// moveItem represents a single source-destination pair for move operations.
type moveItem struct {
	// Source is the source path relative to base_directory.
	Source string `json:"source" jsonschema:"description=Source path relative to base_directory"`
	// Destination is the destination path relative to base_directory.
	Destination string `json:"destination" jsonschema:"description=Destination path relative to base_directory"`
}

// moveFilesRequest represents the input for the move files operation.
type moveFilesRequest struct {
	// Items is the list of source-destination pairs to move.
	Items []moveItem `json:"items" jsonschema:"description=List of source-destination pairs to move"`
	// Overwrite controls whether existing destination files are replaced.
	Overwrite bool `json:"overwrite" jsonschema:"description=Whether to replace existing files at the destination"`
}

// moveFilesResponse represents the output from the move files operation.
type moveFilesResponse struct {
	BaseDirectory string       `json:"base_directory"`
	Results       []itemResult `json:"results"`
	Message       string       `json:"message"`
}

// itemResult represents the result of a single move or copy operation.
type itemResult struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
}

// moveFiles performs the move files operation.
func (f *fileToolSet) moveFiles(
	_ context.Context,
	req *moveFilesRequest,
) (*moveFilesResponse, error) {
	rsp := &moveFilesResponse{
		BaseDirectory: f.baseDir,
	}
	if len(req.Items) == 0 {
		rsp.Message = "Error: items cannot be empty"
		return rsp, fmt.Errorf("items cannot be empty")
	}
	var results []itemResult
	var successCount int
	for _, item := range req.Items {
		result := f.moveSingleItem(item, req.Overwrite)
		results = append(results, result)
		if result.Success {
			successCount++
		}
	}
	rsp.Results = results
	rsp.Message = fmt.Sprintf(
		"Successfully moved %d of %d items",
		successCount,
		len(req.Items),
	)
	return rsp, nil
}

func (f *fileToolSet) moveSingleItem(item moveItem, overwrite bool) itemResult {
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
			"Error: move_files does not support %s:// refs",
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
			"Error: move_files does not support %s:// refs for destination",
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
	if _, err := os.Stat(srcPath); err != nil {
		result.Error = fmt.Sprintf("Error: source not found: %v", err)
		return result
	}
	// Same path check.
	if srcPath == dstPath {
		result.Success = true
		return result
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
	// Move the file or directory.
	if err := os.Rename(srcPath, dstPath); err != nil {
		result.Error = fmt.Sprintf("Error: cannot move: %v", err)
		return result
	}
	result.Success = true
	return result
}

// moveFilesTool returns a callable tool for moving files.
func (f *fileToolSet) moveFilesTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.moveFiles,
		function.WithName("move_files"),
		function.WithDescription(
			"Move or rename files and directories under base_directory. "+
				"Each item specifies a source and destination path. "+
				"Supports moving multiple items in one call.",
		),
	)
}
