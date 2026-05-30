//go:build !windows

package platform

import (
	"context"
	"os"
)

func shell() (ShellSpec, error) {
	for _, p := range []string{"/bin/bash", "/usr/bin/bash", "/usr/local/bin/bash"} {
		if _, err := os.Stat(p); err == nil {
			return ShellSpec{
				Command: p,
				Args:    []string{"-lc"},
			}, nil
		}
	}
	return ShellSpec{
		Command: "sh",
		Args:    []string{"-c"},
	}, nil
}

func buildCommand(_ context.Context, userCommand string) (string, []string, error) {
	s, err := shell()
	if err != nil {
		return "", nil, err
	}
	return s.Command, append(append([]string{}, s.Args...), userCommand), nil
}