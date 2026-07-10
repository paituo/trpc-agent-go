//go:build windows

package fsutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func createSymlink(oldname, newname string) error {
	err := os.Symlink(oldname, newname)
	if err == nil {
		return nil
	}
	if !os.IsPermission(err) {
		return err
	}
	// Resolve to absolute paths for mklink compatibility.
	absOld, absErr := filepath.Abs(oldname)
	if absErr != nil {
		absOld = oldname
	}
	fi, statErr := os.Stat(absOld)
	if statErr != nil {
		// Cannot determine type; return original error.
		return err
	}
	if fi.IsDir() {
		// Directory: use junction (mklink /J) which does not require
		// administrator privileges on Windows 10+.
		return junctionDir(absOld, newname)
	}
	// File: fall back to copy.
	return copyFallback(oldname, newname, fi.Mode().Perm())
}

func junctionDir(target, link string) error {
	absLink, err := filepath.Abs(link)
	if err != nil {
		return err
	}
	// Remove existing path if present.
	_ = os.RemoveAll(link)
	cmd := exec.Command("cmd.exe", "/c", "mklink", "/J", absLink, target)
	if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		return fmt.Errorf("mklink /J %q -> %q: %w: %s",
			absLink, target, cmdErr, out)
	}
	return nil
}

func copyFallback(oldname, newname string, perm os.FileMode) error {
	data, err := os.ReadFile(oldname)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newname), 0o755); err != nil {
		return err
	}
	_ = os.Remove(newname)
	return os.WriteFile(newname, data, perm)
}
