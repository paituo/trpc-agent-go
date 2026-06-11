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
	"slices"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// listFileRequest represents the input for the list file operation.
type listFileRequest struct {
	// Path is a relative directory under base_directory.
	Path string `json:"path" jsonschema:"description=Relative directory path under base_directory or workspace:// directory ref; empty means the base directory"`

	// WithSize returns the size of the files.
	WithSize bool `json:"with_size" jsonschema:"description=Whether to include file sizes in the file list"`

	// Recursive lists files recursively in all subdirectories.
	Recursive bool `json:"recursive" jsonschema:"description=Whether to recursively list files in all subdirectories"`
}

// listFileResponse represents the output from the list file operation.
type listFileResponse struct {
	BaseDirectory string     `json:"base_directory"`
	Path          string     `json:"path"`
	Files         []fileInfo `json:"files"`
	Folders       []string   `json:"folders"`
	Message       string     `json:"message"`
}

type fileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size,omitempty"`
}

// listFile performs the list file operation.
func (f *fileToolSet) listFile(
	ctx context.Context,
	req *listFileRequest,
) (*listFileResponse, error) {
	rsp := &listFileResponse{
		BaseDirectory: f.baseDir,
		Path:          req.Path,
	}

	ref, err := fileref.Parse(req.Path)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	if ref.Scheme == fileref.SchemeArtifact {
		rsp.Message = "Error: listing artifact:// is not supported"
		return rsp, fmt.Errorf("listing artifact:// is not supported")
	}
	if ref.Scheme == fileref.SchemeWorkspace {
		rsp.Path = fileref.WorkspaceRef(ref.Path)
		if req.Recursive {
			rsp.Files, rsp.Folders = listWorkspaceEntriesRecursive(ctx, ref.Path)
		} else {
			rsp.Files, rsp.Folders = listWorkspaceEntries(ctx, ref.Path)
		}
		rsp.Message = listFileSummary(
			len(rsp.Files),
			len(rsp.Folders),
			rsp.Path,
		)
		return rsp, nil
	}

	reqPath := strings.TrimSpace(req.Path)
	rsp.Path = reqPath
	// Resolve the target path.
	targetPath, err := f.resolvePath(reqPath)
	if err != nil {
		rsp.Message = fmt.Sprintf("Error: %v", err)
		return rsp, err
	}
	// Check if the target path exists.
	stat, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			wsFiles, wsFolders := listWorkspaceEntries(ctx, reqPath)
			if len(wsFiles) > 0 || len(wsFolders) > 0 {
				clean := filepath.Clean(reqPath)
				if clean == "." {
					clean = ""
				}
				rsp.Path = fileref.WorkspaceRef(clean)
				rsp.Files = wsFiles
				rsp.Folders = wsFolders
				rsp.Message = listFileSummary(
					len(rsp.Files),
					len(rsp.Folders),
					rsp.Path,
				)
				return rsp, nil
			}
		}
		rsp.Message = fmt.Sprintf(
			"Error: cannot access path '%s': %v",
			reqPath,
			err,
		)
		return rsp, fmt.Errorf("accessing path '%s': %w", reqPath, err)
	}
	// If the target is a file, return information about that file.
	if !stat.IsDir() {
		rsp.Message = fmt.Sprintf(
			"Error: path '%s' is a file, not a directory",
			reqPath,
		)
		return rsp, fmt.Errorf(
			"path '%s' is a file, not a directory",
			reqPath,
		)
	}

	if req.Recursive {
		// Recursively list files in all subdirectories.
		err = filepath.WalkDir(targetPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == targetPath {
				return nil
			}
			relPath, _ := filepath.Rel(targetPath, path)
			relPath = filepath.ToSlash(relPath)
			if d.IsDir() {
				rsp.Folders = append(rsp.Folders, relPath)
				return nil
			}
			fi := fileInfo{Name: relPath}
			if req.WithSize {
				if info, _ := d.Info(); info != nil {
					fi.Size = info.Size()
				}
			}
			rsp.Files = append(rsp.Files, fi)
			return nil
		})
		if err != nil {
			rsp.Message = fmt.Sprintf(
				"Error: cannot recursively read directory '%s': %v",
				reqPath,
				err,
			)
			return rsp, fmt.Errorf(
				"recursively reading directory '%s': %w",
				reqPath,
				err,
			)
		}
	} else {
		// If the target is a directory, list its contents.
		entries, err := os.ReadDir(targetPath)
		if err != nil {
			rsp.Message = fmt.Sprintf(
				"Error: cannot read directory '%s': %v",
				reqPath,
				err,
			)
			return rsp, fmt.Errorf(
				"reading directory '%s': %w",
				reqPath,
				err,
			)
		}
		// Collect files and folders.
		for _, entry := range entries {
			if entry.IsDir() {
				rsp.Folders = append(rsp.Folders, entry.Name())
			} else {
				fi := fileInfo{Name: entry.Name()}
				if req.WithSize {
					if info, _ := entry.Info(); info != nil {
						fi.Size = info.Size()
					}
				}
				rsp.Files = append(rsp.Files, fi)
			}
		}
	}
	// Create a summary message.
	desc := reqPath
	if desc == "" {
		desc = "base directory"
	}
	rsp.Message = listFileSummary(
		len(rsp.Files),
		len(rsp.Folders),
		desc,
	)
	return rsp, nil
}

func listFileSummary(files, folders int, desc string) string {
	if files == 0 && folders == 0 {
		return fmt.Sprintf("Directory '%s' is empty", desc)
	}
	return fmt.Sprintf(
		"Found %d files and %d folders in %s",
		files,
		folders,
		desc,
	)
}

func listWorkspaceEntries(
	ctx context.Context,
	dir string,
) ([]fileInfo, []string) {
	sep := string(filepath.Separator)
	prefix := filepath.Clean(strings.TrimSpace(dir))
	if prefix == "." {
		prefix = ""
	}
	if prefix != "" {
		prefix += sep
	}

	fileSet := make(map[string]struct{})
	foldersSet := make(map[string]struct{})

	for _, f := range fileref.WorkspaceFiles(ctx) {
		name := filepath.Clean(strings.TrimSpace(f.Name))
		if name == "" || name == "." {
			continue
		}
		if prefix != "" {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" || name == "." {
			continue
		}
		name = filepath.ToSlash(name)
		head, _, found := strings.Cut(name, "/")
		prefixSlash := filepath.ToSlash(prefix)
		if !found {
			fileSet[prefixSlash+head] = struct{}{}
			continue
		}
		foldersSet[prefixSlash+head] = struct{}{}
	}

	files := make([]fileInfo, 0, len(fileSet))
	names := make([]string, 0, len(fileSet))
	for n := range fileSet {
		names = append(names, fileref.WorkspaceRef(n))
	}
	slices.Sort(names)
	for _, n := range names {
		files = append(files, fileInfo{Name: n})
	}
	folders := make([]string, 0, len(foldersSet))
	for n := range foldersSet {
		folders = append(folders, fileref.WorkspaceRef(n))
	}
	slices.Sort(folders)
	return files, folders
}

func listWorkspaceEntriesRecursive(
	ctx context.Context,
	dir string,
) ([]fileInfo, []string) {
	sep := string(filepath.Separator)
	prefix := filepath.Clean(strings.TrimSpace(dir))
	if prefix == "." {
		prefix = ""
	}
	if prefix != "" {
		prefix += sep
	}

	fileSet := make(map[string]struct{})
	foldersSet := make(map[string]struct{})

	for _, f := range fileref.WorkspaceFiles(ctx) {
		name := filepath.Clean(strings.TrimSpace(f.Name))
		if name == "" || name == "." {
			continue
		}
		if prefix != "" {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" || name == "." {
			continue
		}
		// In recursive mode, keep the full relative path for files.
		name = filepath.ToSlash(name)
		head, tail, found := strings.Cut(name, "/")
		prefixSlash := filepath.ToSlash(prefix)
		if !found {
			fileSet[prefixSlash+name] = struct{}{}
		} else if tail == "" {
			foldersSet[prefixSlash+head] = struct{}{}
		} else {
			fileSet[prefixSlash+name] = struct{}{}
			// Collect all parent directories.
			parts := strings.Split(name, "/")
			for i := 1; i < len(parts); i++ {
				dirPath := strings.Join(parts[:i], "/")
				foldersSet[prefixSlash+dirPath] = struct{}{}
			}
		}
	}

	files := make([]fileInfo, 0, len(fileSet))
	names := make([]string, 0, len(fileSet))
	for n := range fileSet {
		names = append(names, fileref.WorkspaceRef(n))
	}
	slices.Sort(names)
	for _, n := range names {
		files = append(files, fileInfo{Name: n})
	}
	folders := make([]string, 0, len(foldersSet))
	for n := range foldersSet {
		folders = append(folders, fileref.WorkspaceRef(n))
	}
	slices.Sort(folders)
	return files, folders
}

// listFileTool returns a callable tool for listing file.
func (f *fileToolSet) listFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		f.listFile,
		function.WithName("list_file"),
		function.WithDescription(
			"List files and folders under base_directory. Supports workspace:// paths. "+
				"Set recursive=true to list files in all subdirectories recursively. "+
				"Set with_size=true to include file sizes in the file list.",
		),
	)
}
