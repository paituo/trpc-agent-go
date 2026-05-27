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

package octool

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobObject wraps a Windows Job Object handle for process tree management.
// When KILL_ON_JOB_CLOSE is enabled and the handle is closed, the operating
// system automatically terminates all processes associated with the job.
//
// Close must be called to release the handle. It is safe to call Close
// multiple times (idempotent). However, Close is not safe for concurrent
// use — callers are responsible for serialization.
type jobObject struct {
	handle windows.Handle
}

// newJobObject creates a new unnamed Windows Job Object.
func newJobObject() (*jobObject, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject failed: %w", err)
	}
	return &jobObject{handle: h}, nil
}

// enableKillOnClose sets the JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE flag.
// When the last handle to this job object is closed, the operating system
// terminates all processes associated with the job.
func (j *jobObject) enableKillOnClose() error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err := windows.SetInformationJobObject(
		j.handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		return fmt.Errorf("SetInformationJobObject(KILL_ON_CLOSE) failed: %w", err)
	}
	return nil
}

// assignProcess attaches the given OS process to this Job Object.
// The process must already be running (cmd.Start must have been called).
func (j *jobObject) assignProcess(p *os.Process) error {
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(p.Pid),
	)
	if err != nil {
		return fmt.Errorf("OpenProcess(pid=%d) failed: %w", p.Pid, err)
	}
	defer windows.CloseHandle(h)

	if err := windows.AssignProcessToJobObject(j.handle, h); err != nil {
		return fmt.Errorf("AssignProcessToJobObject(pid=%d) failed: %w", p.Pid, err)
	}
	return nil
}

// close releases the Job Object handle. If KILL_ON_JOB_CLOSE was enabled,
// this causes all associated processes to be terminated by the OS.
// Close is idempotent — multiple calls are safe — but not safe for
// concurrent use. Callers must ensure serialized access.
func (j *jobObject) close() error {
	if j.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(j.handle)
	j.handle = 0
	if err != nil {
		return fmt.Errorf("CloseHandle(JobObject) failed: %w", err)
	}
	return nil
}