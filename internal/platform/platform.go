//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package platform provides cross-platform shell and command abstractions.
package platform

// Shell returns the name of the preferred shell on the current platform.
func Shell() string {
	return shell()
}

// BuildCommand builds a command string suitable for running via the
// platform's shell on the given command line.
func BuildCommand(command string) (name string, args []string) {
	return buildCommand(command)
}