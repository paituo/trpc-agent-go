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

package local

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// localJobObject wraps a Windows Job Object handle for process tree
// management. When KILL_ON_JOB_CLOSE is enabled and the handle is
// closed, the operating system automatically terminates all processes
// associated with the job.
type localJobObject struct {
	mu     sync.Mutex
	handle windows.Handle
}

// newLocalJobObject creates a new unnamed Windows Job Object.
func newLocalJobObject() (*localJobObject, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject failed: %w", err)
	}
	return &localJobObject{handle: h}, nil
}

// enableKillOnClose sets the JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE flag.
func (j *localJobObject) enableKillOnClose() error {
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
func (j *localJobObject) assignProcess(p *os.Process) error {
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

// close releases the Job Object handle. Idempotent and safe for
// concurrent use.
func (j *localJobObject) close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
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