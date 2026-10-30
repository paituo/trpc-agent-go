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

package sandbox

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processJobs tracks the job object created for each started command so
// the whole process tree can be terminated together, mirroring the Unix
// process-group semantics. Without it, killing a wrapper process (for
// example a python3 shim) would leave interpreter descendants running
// and holding the sandbox workspace directory open.
var processJobs sync.Map // *exec.Cmd -> windows.Handle

// setupProcess prepares a kill-on-close job object for the command's
// process tree. The command is assigned to the job after Start through
// attachProcessTree.
func setupProcess(cmd *exec.Cmd) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	processJobs.Store(cmd, job)
}

// attachProcessTree assigns the started process to its job object so
// descendants inherit the job and are terminated with the tree.
func attachProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	value, ok := processJobs.Load(cmd)
	if !ok {
		return
	}
	job := value.(windows.Handle)
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	_ = windows.AssignProcessToJobObject(job, handle)
}

// killProcessGroup terminates the whole process tree via the job object
// when available and always terminates the direct child as well.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if value, ok := processJobs.Load(cmd); ok {
		if err := windows.TerminateJobObject(value.(windows.Handle), 1); err == nil {
			// The job contains the direct child once assigned, so the
			// tree is already down. Kill remains best effort for the
			// small race before assignment completes; a terminated
			// handle surfaces as access-denied and is not a failure.
			_ = cmd.Process.Kill()
			return nil
		}
	}
	return cmd.Process.Kill()
}

// normalizeTimeoutExitCode maps the exit code of a process killed by the
// timeout machinery to the shared -1 "killed" sentinel. Windows has no
// signal delivery: a terminated process reports the termination exit
// code instead, which callers cannot distinguish from a natural exit.
func normalizeTimeoutExitCode(timedOut bool, code int) int {
	if timedOut {
		return -1
	}
	return code
}

// cleanupProcessTree kills any surviving tree members and releases the
// job handle. Closing a job whose members still run terminates them.
func cleanupProcessTree(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if value, ok := processJobs.LoadAndDelete(cmd); ok {
		job := value.(windows.Handle)
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
	}
}
