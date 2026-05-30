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
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type procGroup struct {
	job windows.Handle
}

func newProcGroup() (*procGroup, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject failed: %w", err)
	}

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("SetInformationJobObject failed: %w", err)
	}

	return &procGroup{job: job}, nil
}

func (pg *procGroup) addProcess(pid int) error {
	if pid == 0 {
		return fmt.Errorf("addProcess: pid must be non-zero")
	}

	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return fmt.Errorf("OpenProcess failed (pid=%d): %w", pid, err)
	}
	defer windows.CloseHandle(procHandle)

	if err := windows.AssignProcessToJobObject(pg.job, procHandle); err != nil {
		return fmt.Errorf("AssignProcessToJobObject failed (pid=%d): %w", pid, err)
	}

	return nil
}

func (pg *procGroup) close() error {
	if pg.job == 0 {
		return nil
	}
	if err := windows.CloseHandle(pg.job); err != nil {
		return fmt.Errorf("CloseHandle failed for job object: %w", err)
	}
	pg.job = 0
	return nil
}

var _ = syscall.Errno(0)