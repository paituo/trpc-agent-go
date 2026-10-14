//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package file provides file operation tools for AI agents.
// This tool provides capabilities for saving, reading, listing, searching,
// replacing content, moving, copying, and deleting files in a specified
// base directory.
package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// defaultBaseDir is the default base directory for file operations.
	defaultBaseDir = "."
	// defaultCreateDirMode is the default permission mode for directory
	// (0755: rwxr-xr-x).
	defaultCreateDirMode = os.FileMode(0755)
	// defaultCreateFileMode is the default permission mode for file
	// (0644: rw-r--r--).
	defaultCreateFileMode = os.FileMode(0644)
	// defaultMaxFileSize is the default maximum file size to read, which is 1MB.
	defaultMaxFileSize = 1024 * 1024
	// defaultMaxToolResultChars is the default maximum number of characters
	// returned in a single tool response. This prevents tool results from
	// exceeding LLM provider limits (e.g. DeepSeek 60KB, OpenAI 120KB).
	// 50KB is a safe default that works across most providers.
	defaultMaxToolResultChars = 50 * 1024
	// missingFileHintMaxEntries limits how many top-level entries are
	// suggested when a requested file is missing.
	missingFileHintMaxEntries = 6
)

const (
	inputsDirName = "inputs"

	missingFileEntriesSeparator = ", "
	missingFileDirectorySuffix  = "/"
	missingFileListToolName     = "list_file"
	missingFileSearchToolName   = "search_file"
	missingFileTopLevelPrefix   = "Top-level entries: "
	missingFileBaseDirPrefix    = "Base directory: "
	relativePathGuidance        = "Use a relative path under " +
		"base_directory. If exec_command wrote an absolute temp " +
		"path, fs_read_file can read it only when that path is " +
		"under base_directory or a configured read-only root; " +
		"otherwise have exec_command print the needed data directly."
	extraReadRootGuidance = "Absolute paths are readable only when " +
		"they are under base_directory or a configured read-only root."
	missingFileRecoveryGuidance = "Use " +
		missingFileListToolName + " or " +
		missingFileSearchToolName +
		" to inspect available paths."
	missingFileNoEntriesFallback = "(no visible entries)"
)

// Option is a functional option for configuring the file tool set.
type Option func(*fileToolSet)

// WithBaseDir sets the base directory for file operations, default is
// the current directory.
func WithBaseDir(baseDir string) Option {
	return func(f *fileToolSet) {
		f.baseDir = baseDir
	}
}

// WithSaveFileEnabled enables or disables the save file functionality,
// default is true.
func WithSaveFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.saveFileEnabled = e
	}
}

// WithReadFileEnabled enables or disables the read file functionality,
// default is true.
func WithReadFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.readFileEnabled = e
	}
}

// WithReadMultipleFilesEnabled enables or disables the read multiple
// files functionality, default is true.
func WithReadMultipleFilesEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.readMultipleFilesEnabled = e
	}
}

// WithListFileEnabled enables or disables the list file functionality,
// default is true.
func WithListFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.listFileEnabled = e
	}
}

// WithSearchFileEnabled enables or disables the search file
// functionality, default is true.
func WithSearchFileEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.searchFileEnabled = e
	}
}

// WithSearchContentEnabled enables or disables the search content
// functionality, default is true.
func WithSearchContentEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.searchContentEnabled = e
	}
}

// WithReplaceContentEnabled enables or disables the replace content
// functionality, default is true.
func WithReplaceContentEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.replaceContentEnabled = e
	}
}

// WithMoveFilesEnabled enables or disables the move files functionality,
// default is true.
func WithMoveFilesEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.moveFilesEnabled = e
	}
}

// WithCopyFilesEnabled enables or disables the copy files functionality,
// default is true.
func WithCopyFilesEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.copyFilesEnabled = e
	}
}

// WithDeleteFilesEnabled enables or disables the delete files functionality,
// default is true.
func WithDeleteFilesEnabled(e bool) Option {
	return func(f *fileToolSet) {
		f.deleteFilesEnabled = e
	}
}

// WithCreateDirMode sets the permission mode for creating directory,
// default is 0755 (rwxr-xr-x).
func WithCreateDirMode(m os.FileMode) Option {
	return func(f *fileToolSet) {
		f.createDirMode = m
	}
}

// WithCreateFileMode sets the permission mode for creating file,
// default is 0644 (rw-r--r--).
func WithCreateFileMode(m os.FileMode) Option {
	return func(f *fileToolSet) {
		f.createFileMode = m
	}
}

// WithMaxFileSize sets the maximum file size to read, default is 1MB.
func WithMaxFileSize(s int64) Option {
	return func(f *fileToolSet) {
		f.maxFileSize = s
	}
}

// WithReadOnlyDirs allows read_file and read_multiple_files to read absolute
// paths under the given directories. It does not expand write, replace, list,
// or search permissions.
func WithReadOnlyDirs(dirs ...string) Option {
	return func(f *fileToolSet) {
		f.extraReadRoots = append(f.extraReadRoots, dirs...)
	}
}

// WithMaxToolResultChars sets the maximum number of characters returned
// in a single tool response, default is 50KB (51200). This prevents tool
// results from exceeding LLM provider limits on tool response size.
func WithMaxToolResultChars(n int64) Option {
	return func(f *fileToolSet) {
		f.maxToolResultChars = n
	}
}

// WithName sets the name of the file toolset.
func WithName(name string) Option {
	return func(f *fileToolSet) {
		f.name = name
	}
}

// fileToolSet implements the ToolSet interface for file operations.
type fileToolSet struct {
	baseDir                  string
	extraReadRoots           []string
	readOnlyRootSkipped      map[string]error
	hasInputsDir             bool
	saveFileEnabled          bool
	readFileEnabled          bool
	readMultipleFilesEnabled bool
	listFileEnabled          bool
	searchFileEnabled        bool
	searchContentEnabled     bool
	replaceContentEnabled    bool
	moveFilesEnabled         bool
	copyFilesEnabled         bool
	deleteFilesEnabled       bool
	createDirMode            os.FileMode
	createFileMode           os.FileMode
	maxFileSize              int64
	maxToolResultChars       int64
	tools                    []tool.Tool
	name                     string
}

// Tools implements the ToolSet interface.
func (f *fileToolSet) Tools(ctx context.Context) []tool.Tool {
	return f.tools
}

// Close implements the ToolSet interface.
func (f *fileToolSet) Close() error {
	// No resources to clean up for file tools.
	return nil
}

// Name implements the ToolSet interface.
func (f *fileToolSet) Name() string {
	return f.name
}

// ReadOnlyRootSkipped returns the configured read-only roots that could not be
// used during assembly (missing on disk, or not realpath-resolvable), keyed by
// the original configured value with the reason. It is empty when every
// configured read-only root is usable. Assemblers should surface these via a
// warning so a mis-configured root is observable rather than silently dropped.
func (f *fileToolSet) ReadOnlyRootSkipped() map[string]error {
	return f.readOnlyRootSkipped
}

// ReadOnlyRootsReporter is implemented by tool sets that can report which
// configured read-only roots were skipped during assembly. Assemblers use it to
// emit an observable warning instead of silently dropping unusable roots.
type ReadOnlyRootsReporter interface {
	ReadOnlyRootSkipped() map[string]error
}

// NewToolSet creates a new file operation tool set with the provided
// options.
func NewToolSet(opts ...Option) (tool.ToolSet, error) {
	// Apply default configuration.
	fileToolSet := &fileToolSet{
		baseDir:                  defaultBaseDir,
		saveFileEnabled:          true,
		readFileEnabled:          true,
		readMultipleFilesEnabled: true,
		listFileEnabled:          true,
		searchFileEnabled:        true,
		searchContentEnabled:     true,
		replaceContentEnabled:    true,
		moveFilesEnabled:         true,
		copyFilesEnabled:         true,
		deleteFilesEnabled:       true,
		createDirMode:            defaultCreateDirMode,
		createFileMode:           defaultCreateFileMode,
		maxFileSize:              defaultMaxFileSize,
		maxToolResultChars:       defaultMaxToolResultChars,
		name:                     "file",
	}
	// Apply user-provided options.
	for _, opt := range opts {
		opt(fileToolSet)
	}
	// Clean the base directory and resolve to absolute path
	// to avoid CWD dependency at execution time.
	fileToolSet.baseDir = filepath.Clean(fileToolSet.baseDir)
	absDir, err := filepath.Abs(fileToolSet.baseDir)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to resolve base directory '%s': %w",
			fileToolSet.baseDir,
			err,
		)
	}
	fileToolSet.baseDir = absDir
	fileToolSet.extraReadRoots, fileToolSet.readOnlyRootSkipped = normalizeExtraReadRoots(
		fileToolSet.extraReadRoots,
	)
	// Check if the base directory exists.
	stat, err := os.Stat(fileToolSet.baseDir)
	if err != nil {
		return nil, fmt.Errorf(
			"base directory '%s' does not exist: %w",
			fileToolSet.baseDir,
			err,
		)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf(
			"base directory '%s' is not a directory",
			fileToolSet.baseDir,
		)
	}
	if st, err := os.Stat(
		filepath.Join(fileToolSet.baseDir, inputsDirName),
	); err == nil && st.IsDir() {
		fileToolSet.hasInputsDir = true
	}
	// Create function tools based on enabled features.
	var tools []tool.Tool
	if fileToolSet.saveFileEnabled {
		tools = append(tools, fileToolSet.saveFileTool())
	}
	if fileToolSet.readFileEnabled {
		tools = append(tools, fileToolSet.readFileTool())
	}
	if fileToolSet.readMultipleFilesEnabled {
		tools = append(tools, fileToolSet.readMultipleFilesTool())
	}
	if fileToolSet.listFileEnabled {
		tools = append(tools, fileToolSet.listFileTool())
	}
	if fileToolSet.searchFileEnabled {
		tools = append(tools, fileToolSet.searchFileTool())
	}
	if fileToolSet.searchContentEnabled {
		tools = append(tools, fileToolSet.searchContentTool())
	}
	if fileToolSet.replaceContentEnabled {
		tools = append(tools, fileToolSet.replaceContentTool())
	}
	if fileToolSet.moveFilesEnabled {
		tools = append(tools, fileToolSet.moveFilesTool())
	}
	if fileToolSet.copyFilesEnabled {
		tools = append(tools, fileToolSet.copyFilesTool())
	}
	if fileToolSet.deleteFilesEnabled {
		tools = append(tools, fileToolSet.deleteFilesTool())
	}
	fileToolSet.tools = tools
	return fileToolSet, nil
}

// baseDirFor returns the base directory the fs tools operate on for the given
// invocation context. The default implementation returns the static container
// base directory configured on the tool set, so behaviour is identical to the
// pre-existing static base dir regardless of ctx.
//
// It is the single extension point through which a caller can scope fs
// operations to a per-invocation directory: an overriding implementation may
// derive a narrower base (e.g. a per-session subdirectory) from ctx. All tool
// methods resolve both their reported BaseDirectory and their read/write paths
// through this method so a single override keeps them consistent.
func (f *fileToolSet) baseDirFor(_ context.Context) string {
	return f.baseDir
}

// resolvePath validates a path to prevent directory traversal attacks,
// and resolves the path within the base directory.
// Relative paths are resolved against the base directory.
// Absolute paths that fall within the base directory are accepted.
// It uses the static container base dir (back-compat for direct callers).
func (f *fileToolSet) resolvePath(relativePath string) (string, error) {
	return f.resolvePathCtx(context.Background(), relativePath)
}

// resolvePathCtx is the ctx-aware resolver used by tool methods. It resolves
// the path against the base directory reported by baseDirFor(ctx), which by
// default is the static container base dir.
func (f *fileToolSet) resolvePathCtx(ctx context.Context, relativePath string) (string, error) {
	base := f.baseDirFor(ctx)
	reqPath := f.normalizeInputsAlias(relativePath)
	if strings.Contains(reqPath, "..") {
		return "", fmt.Errorf(
			"invalid path - '..' is not allowed: %s",
			relativePath,
		)
	}
	if filepath.IsAbs(reqPath) {
		rel, err := filepath.Rel(base, reqPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join(base, rel), nil
		}
		return "", fmt.Errorf(
			"invalid path - absolute path outside base directory "+
				"(base: %s): %s. Use a relative path under base_directory. "+
				"If exec_command wrote an absolute temp path, fs_read_file can "+
				"read it only when that path is under base_directory or a "+
				"configured read-only root; otherwise have exec_command print "+
				"the needed data directly.",
			base,
			relativePath,
		)
	}
	return filepath.Join(base, reqPath), nil
}

func (f *fileToolSet) resolveReadPath(requestPath string) (string, error) {
	return f.resolveReadPathCtx(context.Background(), requestPath)
}

func (f *fileToolSet) resolveReadPathCtx(ctx context.Context, requestPath string) (string, error) {
	raw := strings.TrimSpace(requestPath)
	if !filepath.IsAbs(raw) {
		return f.resolvePathCtx(ctx, requestPath)
	}
	clean := filepath.Clean(raw)
	if _, ok := f.matchReadRoot(ctx, clean); ok {
		return clean, nil
	}
	return "", fmt.Errorf(
		"invalid path - absolute path is outside base_directory "+
			"and configured read-only roots: %s. %s %s",
		requestPath,
		extraReadRootGuidance,
		relativePathGuidance,
	)
}

// matchReadRoot reports whether absPath is readable. The base read root is the
// base directory reported by baseDirFor(ctx), consulted before the configured
// read-only roots.
func (f *fileToolSet) matchReadRoot(ctx context.Context, absPath string) (string, bool) {
	if root, ok := f.matchBaseReadRoot(ctx, absPath); ok {
		return root, true
	}
	return f.matchExtraReadRoot(absPath)
}

func (f *fileToolSet) matchBaseReadRoot(ctx context.Context, absPath string) (string, bool) {
	if f == nil {
		return "", false
	}
	base := f.baseDirFor(ctx)
	if strings.TrimSpace(base) == "" {
		return "", false
	}
	root := filepath.Clean(base)
	if !filepath.IsAbs(root) {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return "", false
		}
		root = absRoot
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = filepath.Clean(resolved)
	}
	candidate := filepath.Clean(absPath)
	evaluatedCandidate, resolvedPath := evalPathWithExistingParent(candidate)
	if readRootContains(candidate, evaluatedCandidate, root, resolvedPath) {
		return root, true
	}
	return "", false
}

func (f *fileToolSet) matchExtraReadRoot(absPath string) (string, bool) {
	if f == nil || len(f.extraReadRoots) == 0 {
		return "", false
	}
	candidate := filepath.Clean(absPath)
	evaluatedCandidate, resolvedPath := evalPathWithExistingParent(candidate)
	for _, root := range f.extraReadRoots {
		if readRootContains(
			candidate,
			evaluatedCandidate,
			root,
			resolvedPath,
		) {
			return root, true
		}
	}
	return "", false
}

func readRootContains(
	candidate string,
	evaluatedCandidate string,
	root string,
	resolvedPath bool,
) bool {
	candidateInRoot := isPathWithinRoot(candidate, root)
	evaluatedInRoot := resolvedPath &&
		isPathWithinRoot(evaluatedCandidate, root)
	if candidateInRoot && (!resolvedPath || evaluatedInRoot) {
		return true
	}
	return !candidateInRoot && evaluatedInRoot
}

func evalPathWithExistingParent(path string) (string, bool) {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(resolved), true
	}

	current := clean
	var suffix []string
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			parts := append([]string{resolved}, suffix...)
			return filepath.Clean(filepath.Join(parts...)), true
		}
		current = parent
	}
}

// normalizeExtraReadRoots canonicalizes and validates the configured read-only
// roots. It returns the usable roots plus a map of roots that were configured
// but could not be used (missing on disk, or not realpath-resolvable) together
// with the reason, so callers can surface an observable warning instead of
// silently dropping them.
//
// Relative roots are made absolute against the process working directory
// (which, in the bowei/OpenClaw layout, is the executable directory). This
// makes read_only_dirs portable across machines with the same relative layout,
// instead of hard-coding an absolute machine path.
func normalizeExtraReadRoots(roots []string) ([]string, map[string]error) {
	out := make([]string, 0, len(roots))
	skipped := make(map[string]error, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, raw := range roots {
		root := strings.TrimSpace(raw)
		if root == "" {
			skipped[root] = fmt.Errorf("empty read-only root")
			continue
		}
		if !filepath.IsAbs(root) {
			abs, err := filepath.Abs(root)
			if err != nil {
				skipped[root] = fmt.Errorf("resolve relative root to absolute: %w", err)
				continue
			}
			root = abs
		}
		root = filepath.Clean(root)
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = filepath.Clean(resolved)
		} else {
			// Not realpath-resolvable (missing path, permission, or a reparse
			// point that cannot be followed without privileges on Windows).
			// Still fall through to os.Stat so a plainly-missing root is
			// reported as missing; only keep the root if it physically exists.
			if _, statErr := os.Stat(root); statErr != nil {
				skipped[raw] = fmt.Errorf("read-only root not usable: %w", statErr)
				continue
			}
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	return out, skipped
}

func isPathWithinRoot(candidate string, root string) bool {
	if strings.TrimSpace(candidate) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (f *fileToolSet) normalizeInputsAlias(relativePath string) string {
	if f == nil || f.hasInputsDir {
		return strings.TrimSpace(relativePath)
	}
	raw := strings.TrimSpace(relativePath)
	slashed := filepath.ToSlash(raw)
	const prefix = inputsDirName + "/"
	if slashed == inputsDirName {
		return ""
	}
	if strings.HasPrefix(slashed, prefix) {
		return filepath.FromSlash(strings.TrimPrefix(slashed, prefix))
	}
	return raw
}

func (f *fileToolSet) missingFileHint() string {
	if f == nil {
		return ""
	}

	parts := []string{
		missingFileBaseDirPrefix + f.baseDir,
	}
	if entries := f.topLevelEntriesHint(); entries != "" {
		parts = append(
			parts,
			missingFileTopLevelPrefix+entries,
		)
	}
	parts = append(parts, missingFileRecoveryGuidance)
	return strings.Join(parts, ". ")
}

func (f *fileToolSet) topLevelEntriesHint() string {
	if f == nil || strings.TrimSpace(f.baseDir) == "" {
		return ""
	}

	entries, err := os.ReadDir(f.baseDir)
	if err != nil {
		return ""
	}
	if len(entries) == 0 {
		return missingFileNoEntriesFallback
	}

	names := make([]string, 0, missingFileHintMaxEntries)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += missingFileDirectorySuffix
		}
		names = append(names, name)
		if len(names) >= missingFileHintMaxEntries {
			break
		}
	}
	if len(entries) > len(names) {
		names = append(names, "...")
	}
	return strings.Join(names, missingFileEntriesSeparator)
}

// matchFiles matches files with the given pattern in the target path.
// It returns a list of relative paths, filtered out the "", "." and
// ".." paths.
func (f *fileToolSet) matchFiles(
	targetPath string,
	pattern string,
	caseSensitive bool,
) ([]string, error) {
	if pattern == "" {
		return nil, errors.New("pattern cannot be empty")
	}
	opts := []doublestar.GlobOption{}
	if !caseSensitive {
		opts = append(opts, doublestar.WithCaseInsensitive())
	}
	matches, err := doublestar.Glob(os.DirFS(targetPath), pattern, opts...)
	if err != nil {
		return nil, fmt.Errorf(
			"searching files with pattern '%s': %w",
			pattern,
			err,
		)
	}
	files := matches[:0]
	for _, match := range matches {
		if match == "" || match == "." || match == ".." {
			continue
		}
		files = append(files, match)
	}
	return files, nil
}
