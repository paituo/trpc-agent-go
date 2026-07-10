//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package fsutil provides cross-platform filesystem utilities.
package fsutil

// CreateSymlink creates newname as a symbolic link (or equivalent)
// to oldname. On Windows, if the caller lacks the required
// SeCreateSymbolicLinkPrivilege, it falls back to a directory
// junction (for directories) or a plain copy (for files).
func CreateSymlink(oldname, newname string) error {
	return createSymlink(oldname, newname)
}
