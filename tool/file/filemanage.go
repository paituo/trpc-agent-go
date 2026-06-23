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

// fileManageAction represents the action to perform.
type fileManageAction string

const (
	actionMove   fileManageAction = "move"
	actionCopy   fileManageAction = "copy"
	actionDelete fileManageAction = "delete"
)

// fileManageItem represents a single source-destination pair for move/copy operations.
type fileManageItem struct {
	// Source is the source path relative to base_directory.
	Source string `json:"source" jsonschema:"description=Source path relative to base_directory,required"`
	// Destination is the destination path relative to base_directory.
	Destination string `json:"destination" jsonschema:"description=Destination path relative to base_directory,required"`
}

// fileManageRequest represents the input for the file manage operation.
type fileManageRequest struct {
	// Action is the operation to perform: "move", "copy", or "delete".
	Action string `json:"action" jsonschema:"description=Operation to perform,enum=move,enum=copy,enum=delete"`
	// Items is the list of source-destination pairs for move/copy actions.
	// Each item must have both "source" and "destination" fields.
	// Required when action is "move" or "copy".
	Items []fileManageItem `json:"items,omitempty" jsonschema:"description=List of items for move/copy. Each item must have 'source' and 'destination'. Required for move/copy actions"`
	// Paths is the list of paths to delete for delete action.
	// Required when action is "delete".
	Paths []string `json:"paths,omitempty" jsonschema:"description=List of paths to delete. Required for delete action"`
	// Overwrite controls whether existing destination files are replaced (for move/copy actions).
	Overwrite bool `json:"overwrite" jsonschema:"description=Whether to replace existing files at the destination (default false). Only used for move/copy actions"`
}

// fileManageResult represents the result of a single operation.
type fileManageResult struct {
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	Path        string `json:"path,omitempty"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
}

// fileManageResponse represents the output from the file manage operation.
type fileManageResponse struct {
	BaseDirectory string             `json:"base_directory"`
	Action        string             `json:"action"`
	Results       []fileManageResult `json:"results"`
	Message       string             `json:"message"`
}

// fileManage performs the file manage operation.
func (f *fileToolSet) fileManage(
	_ context.Context,
	req *fileManageRequest,
) (*fileManageResponse, error) {
	rsp := &fileManageResponse{
		BaseDirectory: f.baseDir,
		Action:        req.Action,
	}

	switch req.Action {
	case "move":
		if !f.fileManageMoveEnabled {
			rsp.Message = "Error: move action is disabled"
			return rsp, fmt.Errorf("move action is disabled")
		}
		return f.fileManageMove(req, rsp)
	case "copy":
		if !f.fileManageCopyEnabled {
			rsp.Message = "Error: copy action is disabled"
			return rsp, fmt.Errorf("copy action is disabled")
		}
		return f.fileManageCopy(req, rsp)
	case "delete":
		if !f.fileManageDeleteEnabled {
			rsp.Message = "Error: delete action is disabled"
			return rsp, fmt.Errorf("delete action is disabled")
		}
		return f.fileManageDelete(req, rsp)
	default:
		rsp.Message = fmt.Sprintf(
			"Error: unsupported action '%s'. Supported actions: move, copy, delete",
			req.Action,
		)
		return rsp, fmt.Errorf(
			"unsupported action '%s'", req.Action,
		)
	}
}

func (f *fileToolSet) fileManageMove(
	req *fileManageRequest,
	rsp *fileManageResponse,
) (*fileManageResponse, error) {
	if len(req.Items) == 0 {
		rsp.Message = "Error: items cannot be empty for move action"
		return rsp, fmt.Errorf("items cannot be empty for move action")
	}
	var results []fileManageResult
	var successCount int
	for _, item := range req.Items {
		result := f.fileManageMoveItem(item, req.Overwrite)
		results = append(results, fileManageResult{
			Source:      result.Source,
			Destination: result.Destination,
			Success:     result.Success,
			Error:       result.Error,
		})
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

func (f *fileToolSet) fileManageCopy(
	req *fileManageRequest,
	rsp *fileManageResponse,
) (*fileManageResponse, error) {
	if len(req.Items) == 0 {
		rsp.Message = "Error: items cannot be empty for copy action"
		return rsp, fmt.Errorf("items cannot be empty for copy action")
	}
	var results []fileManageResult
	var successCount int
	for _, item := range req.Items {
		result := f.fileManageCopyItem(item, req.Overwrite)
		results = append(results, fileManageResult{
			Source:      result.Source,
			Destination: result.Destination,
			Success:     result.Success,
			Error:       result.Error,
		})
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

func (f *fileToolSet) fileManageDelete(
	req *fileManageRequest,
	rsp *fileManageResponse,
) (*fileManageResponse, error) {
	if len(req.Paths) == 0 {
		rsp.Message = "Error: paths cannot be empty for delete action"
		return rsp, fmt.Errorf("paths cannot be empty for delete action")
	}
	var results []fileManageResult
	var successCount int
	for _, path := range req.Paths {
		result := f.fileManageDeletePath(path)
		results = append(results, fileManageResult{
			Path:    result.Path,
			Success: result.Success,
			Error:   result.Error,
		})
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

// fileManageMoveItem performs a single move operation.
func (f *fileToolSet) fileManageMoveItem(item fileManageItem, overwrite bool) itemResult {
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
			"Error: move does not support %s:// refs",
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
			"Error: move does not support %s:// refs for destination",
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

// fileManageCopyItem performs a single copy operation.
func (f *fileToolSet) fileManageCopyItem(item fileManageItem, overwrite bool) itemResult {
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
			"Error: copy does not support %s:// refs",
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
			"Error: copy does not support %s:// refs for destination",
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
		if err := fileManageCopyDirectory(srcPath, dstPath); err != nil {
			result.Error = fmt.Sprintf(
				"Error: cannot copy directory: %v",
				err,
			)
			return result
		}
	} else {
		if err := fileManageCopyFile(srcPath, dstPath); err != nil {
			result.Error = fmt.Sprintf("Error: cannot copy file: %v", err)
			return result
		}
	}
	result.Success = true
	return result
}

// fileManageDeletePath performs a single delete operation.
func (f *fileToolSet) fileManageDeletePath(path string) deleteResult {
	result := deleteResult{Path: path}
	// Reject file refs.
	ref, err := fileref.Parse(path)
	if err != nil {
		result.Error = fmt.Sprintf("Error: %v", err)
		return result
	}
	if ref.Scheme != "" {
		result.Error = fmt.Sprintf(
			"Error: delete does not support %s:// refs",
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

// fileManageCopyDirectory recursively copies a directory from src to dst.
func fileManageCopyDirectory(src, dst string) error {
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
			if err := fileManageCopyDirectory(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := fileManageCopyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// fileManageCopyFile copies a single file from src to dst, preserving permissions.
func fileManageCopyFile(src, dst string) error {
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

// fileManageTool returns a callable tool for file management operations.
func (f *fileToolSet) fileManageTool() tool.CallableTool {
	desc := "Manage files and directories under base_directory."
	var actions []string
	if f.fileManageMoveEnabled {
		actions = append(actions, "'move' (move/rename files and directories)")
	}
	if f.fileManageCopyEnabled {
		actions = append(actions, "'copy' (copy files and directories recursively)")
	}
	if f.fileManageDeleteEnabled {
		actions = append(actions, "'delete' (delete files and directories recursively)")
	}
	if len(actions) > 0 {
		desc += " Supports actions: " + strings.Join(actions, ", ") + "."
	}
	desc += " Parameter rules by action:"
	if f.fileManageMoveEnabled {
		desc += " For 'move': provide 'items' (array of {source, destination}) and optionally 'overwrite'."
	}
	if f.fileManageCopyEnabled {
		desc += " For 'copy': provide 'items' (array of {source, destination}) and optionally 'overwrite'."
	}
	if f.fileManageDeleteEnabled {
		desc += " For 'delete': provide 'paths' (array of path strings)."
	}
	return function.NewFunctionTool(
		f.fileManage,
		function.WithName("file_manage"),
		function.WithDescription(desc),
	)
}
