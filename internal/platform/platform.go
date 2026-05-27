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

import "context"

// ShellSpec describes the command and arguments for a platform shell.
type ShellSpec struct {
	Command string   // e.g. "bash" or "powershell.exe"
	Args    []string // e.g. ["-lc"] or ["-NoProfile", "-NonInteractive", "-Command"]
}

// Shell returns the OS-appropriate shell specification.
// On Unix: prefers bash, falls back to sh.
// On Windows: prefers PowerShell, falls back to cmd.exe.
// WSL bash, Git Bash, Cygwin, and MSYS2 are never returned.
func Shell() (ShellSpec, error)

// BuildCommand builds the OS command that runs userCommand through the
// shell. It is a convenience wrapper around Shell().
//
// The ctx parameter may be used for cancellation during shell detection.
//
// It returns the executable path (cmd), the combined shell arguments
// followed by the user command (args), and any error encountered (err).
func BuildCommand(ctx context.Context, userCommand string) (cmd string, args []string, err error)