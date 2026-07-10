//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package fsutil

import (
	"os"
)

// IsLink reports whether name is a symlink or, on Windows, a
// directory junction.
func IsLink(name string) (bool, error) {
	return isLink(name)
}

// isSymlink is used internally by IsLink to check os.ModeSymlink.
func isSymlink(name string) (bool, error) {
	fi, err := os.Lstat(name)
	if err != nil {
		return false, err
	}
	return fi.Mode()&os.ModeSymlink != 0, nil
}
