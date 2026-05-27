//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Shell returns the OS-appropriate shell for Windows.
//
// Design decisions (v3, user decision):
//   - PowerShell is the default (richer scripting, object pipeline, better error handling)
//   - cmd.exe is the fallback when PowerShell is unavailable
//   - WSL bash/sh are ALWAYS excluded (requires HCS, unreliable)
//   - Git Bash/Cygwin/MSYS2 bash are ALWAYS excluded (path/encoding incompatibility)
//
// WSL bash appears at C:\Windows\System32\bash.exe and can be found by
// exec.LookPath("bash"), but it requires HCS (Host Compute Service) to
// be running. If HCS is down, bash.exe fails with:
//
//	Bash/Service/CreateInstance/CreateVm/HCS/HCS_E_SERVICE_NOT_AVAILABLE
func Shell() (ShellSpec, error) {
	// 1. PowerShell (pwsh.exe on new systems, powershell.exe on older)
	for _, name := range []string{"powershell.exe", "pwsh.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			if !isNonNativeShell(p) {
				return ShellSpec{
					Command: p,
					Args:    []string{"-NoProfile", "-NonInteractive", "-Command"},
				}, nil
			}
		}
	}
	// 2. cmd.exe — always available, last resort
	if p, err := exec.LookPath("cmd.exe"); err == nil {
		if !isNonNativeShell(p) {
			return ShellSpec{
				Command: p,
				Args:    []string{"/d", "/s", "/c"},
			}, nil
		}
	}
	return ShellSpec{}, fmt.Errorf("no usable shell found on Windows")
}

// isNonNativeShell detects whether the given executable path belongs to
// a non-native shell environment (WSL, Git Bash, Cygwin, MSYS2).
//
// These shells should NOT be used as a general-purpose command executor:
//   - WSL bash: requires Hyper-V Host Compute Service (HCS)
//   - Git Bash (MinGW/MSYS2): uses Unix-style paths incompatible with
//     Windows-native tools
//   - Cygwin: similar path compatibility issues
func isNonNativeShell(p string) bool {
	p = strings.ToLower(filepath.Clean(p))
	sysRoot := strings.ToLower(os.Getenv("SystemRoot"))
	if sysRoot == "" {
		sysRoot = `c:\windows`
	}

	// WSL bash.exe / sh.exe in System32 or SysWOW64
	wslBashSystem32 := filepath.Join(sysRoot, "system32", "bash.exe")
	wslBashSysWOW64 := filepath.Join(sysRoot, "syswow64", "bash.exe")
	if p == wslBashSystem32 || p == wslBashSysWOW64 {
		return true
	}

	// WSL internal paths
	if strings.Contains(p, "lxss") {
		return true
	}

	// Git Bash / MSYS2 / Cygwin — detect by well-known install paths
	nonNativeShellSuffixes := []string{
		`\git\bin\bash.exe`,
		`\git\usr\bin\bash.exe`,
		`\msys64\usr\bin\bash.exe`,
		`\cygwin64\bin\bash.exe`,
		`\cygwin\bin\bash.exe`,
	}
	for _, suffix := range nonNativeShellSuffixes {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}

	return false
}

// BuildCommand builds a shell command for Windows.
func BuildCommand(_ context.Context, userCommand string) (string, []string, error) {
	s, err := Shell()
	if err != nil {
		return "", nil, err
	}
	return s.Command, append(append([]string{}, s.Args...), userCommand), nil
}