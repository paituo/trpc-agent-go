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
	"os"
	"testing"
)

func TestShell_PrefersPowerShell(t *testing.T) {
	spec, err := Shell()
	if err != nil {
		t.Fatalf("Shell() returned error: %v", err)
	}
	t.Logf("Shell: %s %v", spec.Command, spec.Args)
}

func TestShell_ExcludesWSLPaths(t *testing.T) {
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}

	tests := []struct {
		path     string
		expected bool
	}{
		{sysRoot + `\System32\bash.exe`, true},
		{sysRoot + `\SysWOW64\bash.exe`, true},
		{sysRoot + `\System32\cmd.exe`, false},
		{sysRoot + `\System32\WindowsPowerShell\v1.0\powershell.exe`, false},
	}

	for _, tt := range tests {
		result := isNonNativeShell(tt.path)
		if result != tt.expected {
			t.Errorf("isNonNativeShell(%q) = %v, want %v", tt.path, result, tt.expected)
		}
	}
}

func TestShell_ExcludesNonNativeShellPaths(t *testing.T) {
	tests := []struct {
		path     string
		desc     string
		expected bool
	}{
		{`C:\Program Files\Git\bin\bash.exe`, "Git for Windows", true},
		{`D:\tools\msys64\usr\bin\bash.exe`, "MSYS2", true},
		{`E:\cygwin64\bin\bash.exe`, "Cygwin 64", true},
		{`C:\cygwin\bin\bash.exe`, "Cygwin 32", true},
		{`C:\Windows\System32\cmd.exe`, "Native cmd", false},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, "PowerShell 7", false},
	}

	for _, tt := range tests {
		result := isNonNativeShell(tt.path)
		if result != tt.expected {
			t.Errorf("isNonNativeShell(%q) [%s] = %v, want %v", tt.path, tt.desc, result, tt.expected)
		}
	}
}