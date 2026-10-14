//go:build windows

package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// shouldDegradeSymlink reports whether a failed os.Symlink should fall
// back to a junction (directory) or copy (file). Beyond the usual
// permission errors, Windows reports ERROR_PRIVILEGE_NOT_HELD (1314)
// when the caller lacks SeCreateSymbolicLinkPrivilege; os.IsPermission
// does not recognize that code, so we must match it explicitly.
func shouldDegradeSymlink(err error) bool {
	if os.IsPermission(err) {
		return true // ERROR_ACCESS_DENIED / EPERM
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case 1314: // ERROR_PRIVILEGE_NOT_HELD
			return true
		case syscall.EPERM:
			return true
		}
	}
	return false
}

func createSymlink(oldname, newname string) error {
	err := os.Symlink(oldname, newname)
	if err == nil {
		return nil
	}
	if !shouldDegradeSymlink(err) {
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
		// Directory: first try a junction (mklink /J) which does not
		// require administrator privileges on Windows 10+. If that
		// also fails (e.g. mklink unavailable), fall back to a full
		// directory copy so the link target is still materialized.
		if jErr := junctionDir(absOld, newname); jErr == nil {
			return nil
		}
		return copyDirFallback(absOld, newname, fi.Mode().Perm())
	}
	// File: fall back to copy.
	return copyFallback(oldname, newname, fi.Mode().Perm())
}

// copyDirFallback recursively copies a directory tree as the second-level
// degradation after junction creation fails. It ensures the caller still
// gets a usable materialized target without any symlink/junction privilege.
func copyDirFallback(src, dst string, perm os.FileMode) error {
	_ = os.RemoveAll(dst)
	if err := os.MkdirAll(dst, perm|0o700); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == src {
			return nil
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, perm|0o700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, linkErr := os.Readlink(p)
			if linkErr != nil {
				return linkErr
			}
			return os.Symlink(link, target)
		}
		return copyFile(p, target, perm)
	})
}

// copyFile copies a single regular file from src to dst with the given
// permissions, creating the parent directory as needed.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
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
