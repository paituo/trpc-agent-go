//go:build !windows

package deps

import "os/exec"

// hideChildConsole 在非 Windows 平台无操作（无控制台窗口概念）。
func hideChildConsole(cmd *exec.Cmd) {}
