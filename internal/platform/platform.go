//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package platform provides cross-platform abstractions for command execution.
package platform

// ShellSpec describes the command and arguments for a platform shell.
type ShellSpec struct {
	Command string   // e.g. "bash" or "powershell.exe"
	Args    []string // e.g. ["-lc"] or ["-NoProfile", "-NonInteractive", "-Command"]
}