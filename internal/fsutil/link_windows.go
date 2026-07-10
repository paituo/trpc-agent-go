//go:build windows

package fsutil

import (
	"os"
	"syscall"
)

func isLink(name string) (bool, error) {
	ok, err := isSymlink(name)
	if err != nil || ok {
		return ok, err
	}
	// os.Lstat does not expose junctions as symlinks on Windows.
	// Fall back to checking the reparse-point attribute.
	fi, statErr := os.Lstat(name)
	if statErr != nil {
		return false, statErr
	}
	return isReparsePoint(fi), nil
}

// isReparsePoint checks if the file has FILE_ATTRIBUTE_REPARSE_POINT.
// Junctions, symlinks, and mount points are all reparse points.
// We call this after isSymlink already returned false, so this
// effectively detects junctions.
func isReparsePoint(fi os.FileInfo) bool {
	// On Windows, the raw syscall.Win32FileAttributeData contains
	// FILE_ATTRIBUTE_REPARSE_POINT (0x400). We use Sys() to access
	// the underlying data.
	attr, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	return attr.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
