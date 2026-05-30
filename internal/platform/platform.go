//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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

// Shell returns the OS-appropriate shell spec for the current platform.
func Shell() (ShellSpec, error) {
	return shell()
}

// BuildCommand builds a command string suitable for running via the
// platform's shell on the given command line.
func BuildCommand(ctx context.Context, userCommand string) (name string, args []string, err error) {
	return buildCommand(ctx, userCommand)
}