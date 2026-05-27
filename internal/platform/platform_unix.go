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

// Shell returns bash or sh on Unix-like systems.
func Shell() (ShellSpec, error) {
	if path, err := exec.LookPath("bash"); err == nil {
		return ShellSpec{Command: path, Args: []string{"-lc"}}, nil
	}
	if path, err := exec.LookPath("sh"); err == nil {
		return ShellSpec{Command: path, Args: []string{"-lc"}}, nil
	}
	return ShellSpec{}, errors.New("bash or sh is required")
}

// BuildCommand builds a shell command for Unix-like systems.
func BuildCommand(_ context.Context, userCommand string) (string, []string, error) {
	s, err := Shell()
	if err != nil {
		return "", nil, err
	}
	return s.Command, append(s.Args, userCommand), nil
}