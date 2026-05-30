//go:build windows

package platform

import (
	"os"
	"os/exec"
	"strings"
)

func shell() string {
	pwsh := findExe("pwsh.exe")
	if pwsh != "" {
		return pwsh
	}
	ps := findExe("powershell.exe")
	if ps != "" {
		return ps
	}
	return "cmd"
}

func buildCommand(command string) (string, []string) {
	sh := shell()
	if strings.HasSuffix(sh, "powershell.exe") || strings.HasSuffix(sh, "pwsh.exe") {
		return sh, []string{"-NoProfile", "-NonInteractive", "-Command", command}
	}
	return sh, []string{"/C", command}
}

func findExe(name string) string {
	paths := []string{
		`C:\Program Files\PowerShell\7\` + name,
		`C:\Windows\System32\WindowsPowerShell\v1.0\` + name,
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if lp, err := exec.LookPath(name); err == nil {
		if strings.Contains(strings.ToLower(lp), "git") ||
			strings.Contains(strings.ToLower(lp), "msys") ||
			strings.Contains(strings.ToLower(lp), "cygwin") {
			return ""
		}
		return lp
	}
	return ""
}