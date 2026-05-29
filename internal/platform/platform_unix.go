//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !windows

package platform

import (
	"context"
	"errors"
	"os/exec"
)

// Shell returns the OS-appropriate shell specification.
// On Unix: prefers bash, falls back to sh.
// WSL bash, Git Bash, Cygwin, and MSYS2 are never returned.
func Shell() (ShellSpec, error) {
	if path, err := exec.LookPath("bash"); err == nil {
		return ShellSpec{Command: path, Args: []string{"-lc"}}, nil
	}
	if path, err := exec.LookPath("sh"); err == nil {
		return ShellSpec{Command: path, Args: []string{"-lc"}}, nil
	}
	return ShellSpec{}, errors.New("bash or sh is required")
}

// BuildCommand builds the OS command that runs userCommand through the
// shell. It is a convenience wrapper around Shell().
//
// The ctx parameter may be used for cancellation during shell detection.
//
// It returns the executable path (cmd), the combined shell arguments
// followed by the user command (args), and any error encountered (err).
func BuildCommand(_ context.Context, userCommand string) (string, []string, error) {
	s, err := Shell()
	if err != nil {
		return "", nil, err
	}
	return s.Command, append(append([]string{}, s.Args...), userCommand), nil
}