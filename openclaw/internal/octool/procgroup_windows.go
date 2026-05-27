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

// kernel32 DLL for Job Object API.
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

var (
	procCreateJobObjectW          = kernel32.NewProc("CreateJobObjectW")
	procAssignProcessToJobObject  = kernel32.NewProc("AssignProcessToJobObject")
	procSetInformationJobObject   = kernel32.NewProc("SetInformationJobObject")
)

const (
	// jobObjectLimitKillOnJobClose causes all processes associated with the
	// Job Object to be terminated when the last handle is closed.
	jobObjectLimitKillOnJobClose = 0x2000

	// jobObjectExtendedLimitInformation is the information class for
	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
	jobObjectExtendedLimitInformation = 9
)

// jobObject wraps a Windows Job Object handle.
// Must be released via close() which also triggers KILL_ON_JOB_CLOSE if enabled.
type jobObject struct {
	handle windows.Handle
}

// newJobObject creates a new unnamed Job Object.
func newJobObject() (*jobObject, error) {
	name, _ := windows.UTF16PtrFromString("")
	h, _, err := procCreateJobObjectW.Call(
		0, // lpJobAttributes = NULL (default security)
		uintptr(unsafe.Pointer(name)),
	)
	if h == 0 {
		return nil, fmt.Errorf("CreateJobObjectW failed: %w", err)
	}
	return &jobObject{handle: windows.Handle(h)}, nil
}

// enableKillOnClose sets JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
// When the last handle to this Job Object is closed, all associated processes
// will be terminated.
func (j *jobObject) enableKillOnClose() error {
	type jobObjectExtendedLimitInfo struct {
		basicLimit  windows.JOBOBJECT_BASIC_LIMIT_INFORMATION
		ioInfo      windows.IO_COUNTERS
		processMem  uintptr
		jobMem      uintptr
		peakProcess uintptr
		peakJob     uintptr
	}

	var info jobObjectExtendedLimitInfo
	info.basicLimit.LimitFlags = jobObjectLimitKillOnJobClose

	ret, _, err := procSetInformationJobObject.Call(
		uintptr(j.handle),
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ret == 0 {
		return fmt.Errorf("SetInformationJobObject(KILL_ON_CLOSE) failed: %w", err)
	}
	return nil
}

// assignProcess assigns the given OS process to this Job Object.
// The process must already be running (Start already called).
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

	ret, _, err := procAssignProcessToJobObject.Call(
		uintptr(j.handle),
		uintptr(h),
	)
	if ret == 0 {
		return fmt.Errorf("AssignProcessToJobObject(pid=%d) failed: %w", p.Pid, err)
	}
	return nil
}

// close releases the Job Object handle. If KILL_ON_JOB_CLOSE was set,
// this triggers termination of all associated processes.
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