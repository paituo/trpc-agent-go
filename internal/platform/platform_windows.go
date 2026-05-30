//go:build windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func shell() (ShellSpec, error) {
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
	if p, err := exec.LookPath("cmd.exe"); err == nil {
		if !isNonNativeShell(p) {
			return ShellSpec{
				Command: p,
				Args:    []string{"/d", "/s", "/c"},
			}, nil
		}
	}
	return ShellSpec{}, errors.New("no usable shell found on Windows")
}

func isNonNativeShell(p string) bool {
	p = strings.ToLower(filepath.Clean(p))
	sysRoot := strings.ToLower(os.Getenv("SystemRoot"))
	if sysRoot == "" {
		sysRoot = `c:\windows`
	}

	wslBashSystem32 := filepath.Join(sysRoot, "system32", "bash.exe")
	wslBashSysWOW64 := filepath.Join(sysRoot, "syswow64", "bash.exe")
	if p == wslBashSystem32 || p == wslBashSysWOW64 {
		return true
	}

	if strings.Contains(p, "lxss") {
		return true
	}

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

func buildCommand(_ context.Context, userCommand string) (string, []string, error) {
	s, err := shell()
	if err != nil {
		return "", nil, err
	}
	return s.Command, append(append([]string{}, s.Args...), userCommand), nil
}