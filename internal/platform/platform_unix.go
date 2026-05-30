//go:build !windows

package platform

import "os"

func shell() string {
	for _, p := range []string{"/bin/bash", "/usr/bin/bash", "/usr/local/bin/bash"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "sh"
}

func buildCommand(command string) (string, []string) {
	return shell(), []string{"-c", command}
}