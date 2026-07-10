//go:build !windows

package fsutil

import "os"

func createSymlink(oldname, newname string) error {
	return os.Symlink(oldname, newname)
}
