//go:build !windows

package fsutil

func isLink(name string) (bool, error) {
	return isSymlink(name)
}
