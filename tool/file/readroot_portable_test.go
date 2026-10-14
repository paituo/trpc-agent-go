//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package file

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// read_only_dirs portable config + observable skip (fs-read-root-portable-config)
// ---------------------------------------------------------------------------

// TestNormalizeExtraReadRoots_RelativeAbsolutized ensures a relative read-only
// root is absolutized against the process CWD (so portable relative-to-exe
// configs work) and a missing relative root lands in skipped.
func TestNormalizeExtraReadRoots_RelativeAbsolutized(t *testing.T) {
	// '.' is a path that always exists and is trivially relative.
	roots, skipped := normalizeExtraReadRoots([]string{"."})
	require.Len(t, roots, 1, "usable root count")
	assert.True(t, filepath.IsAbs(roots[0]), "relative root must be absolutized, got %q", roots[0])
	assert.Len(t, skipped, 0, "a relative root that exists must not be skipped")

	// A non-existent relative path must be skipped, not silently dropped.
	missingRel := filepath.Join("__definitely_missing__", "rel_root")
	roots2, skipped2 := normalizeExtraReadRoots([]string{missingRel})
	assert.Empty(t, roots2, "missing relative root must not be usable")
	require.Len(t, skipped2, 1, "missing relative root must be reported")
	assert.Contains(t, skipped2[missingRel].Error(), "not usable")
}

// TestNormalizeExtraReadRoots_AbsolutePreserved ensures an absolute read-only
// root is preserved verbatim (backward compatibility with existing configs).
func TestNormalizeExtraReadRoots_AbsolutePreserved(t *testing.T) {
	abs := t.TempDir()
	roots, skipped := normalizeExtraReadRoots([]string{abs})
	require.Len(t, roots, 1)
	assert.Equal(t, abs, roots[0])
	assert.Empty(t, skipped)

	// An absolute path that does not exist is skipped with a reason.
	missingAbs := filepath.Join(t.TempDir(), "nope")
	roots2, skipped2 := normalizeExtraReadRoots([]string{missingAbs})
	assert.Empty(t, roots2)
	require.Len(t, skipped2, 1)
	assert.Contains(t, skipped2[missingAbs].Error(), "not usable")
}

// TestNormalizeExtraReadRoots_EmptyAndBlank ensures blank entries are skipped
// (normalized to the "" key) rather than kept, and do not break remaining roots.
func TestNormalizeExtraReadRoots_EmptyAndBlank(t *testing.T) {
	abs := t.TempDir()
	roots, skipped := normalizeExtraReadRoots([]string{"", "  ", abs})
	require.Len(t, roots, 1)
	assert.Equal(t, abs, roots[0])
	require.Len(t, skipped, 1, "blank entries normalize to a single \"\" skip entry")
	assert.Contains(t, skipped[""].Error(), "empty read-only root")
}

// TestNewToolSet_ReadOnlyRootSkipped_Reported ensures the assembled tool set
// exposes skipped read-only roots through the reporter interface, so the
// assembler can warn instead of the configuration silently not applying.
func TestNewToolSet_ReadOnlyRootSkipped_Reported(t *testing.T) {
	base := t.TempDir()
	good := t.TempDir()
	missing := filepath.Join(base, "no_such_dir")

	set, err := NewToolSet(WithBaseDir(base), WithReadOnlyDirs(good, missing))
	require.NoError(t, err)

	reporter, ok := set.(ReadOnlyRootsReporter)
	require.True(t, ok, "fileToolSet must implement ReadOnlyRootsReporter")

	skipped := reporter.ReadOnlyRootSkipped()
	require.Len(t, skipped, 1, "only the missing root is skipped")
	reason, exists := skipped[missing]
	require.True(t, exists, "missing read-only root keyed by configured value")
	assert.Contains(t, reason.Error(), "not usable")

	// The usable root must still be matched for access.
	fts := set.(*fileToolSet)
	assert.Contains(t, fts.extraReadRoots, good)
}

// TestNormalizeExtraReadRoots_RejectNonFile ensures a configured root that is a
// regular file (not a directory) is still kept when it exists (matching prior
// behavior); this guards against over-tightening scope.
func TestNormalizeExtraReadRoots_ExistingAny(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0644))
	roots, skipped := normalizeExtraReadRoots([]string{file})
	assert.Empty(t, skipped)
	require.Len(t, roots, 1)
	assert.True(t, strings.HasSuffix(roots[0], "plain.txt"))
}
