//go:build !windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"os/exec"
	"syscall"
)

func setupProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// attachProcessTree is a no-op outside Windows: setpgid in setupProcess
// already covers descendants through the process group.
func attachProcessTree(*exec.Cmd) {}

// normalizeTimeoutExitCode keeps the raw exit code: Unix kills deliver a
// signal, and exec already reports killed processes as -1.
func normalizeTimeoutExitCode(_ bool, code int) int { return code }

func cleanupProcessTree(cmd *exec.Cmd) {
	_ = killProcessGroup(cmd)
}
