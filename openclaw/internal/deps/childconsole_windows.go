//go:build windows

package deps

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW 让子进程不创建/不显示控制台窗口。
//
// 背景：deps 启动探测（InspectStartup→DetectToolchain→FindPythonRuntime）会拉起
// python/cmd 等控制台子系统程序（当解析路径命中 Windows 的 App Execution Alias
// 0 字节重定向器时还会经 AppInstallerPythonRedirector.exe 中转）。若无此标志，
// bowei-openclaw / bowei-work-agent 启动时就会弹出一个黑色终端窗口一闪而过。
// 此实现与 codeexecutor/local、localpython 的既有做法保持一致。
func hideChildConsole(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
