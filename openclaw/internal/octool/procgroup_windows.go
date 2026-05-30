//go:build windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package octool

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	_ = syscall.Errno(0)
)

type procGroup struct {
	mu   sync.Mutex
	job  windows.Handle
	done bool
}

func newProcGroup() (*procGroup, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject failed: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("SetInformationJobObject failed: %w", err)
	}
	return &procGroup{job: job}, nil
}

func (pg *procGroup) addProcess(pid int) error {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if pg.done {
		return nil
	}
	if pid == 0 {
		return fmt.Errorf("addProcess: pid must be non-zero")
	}
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return fmt.Errorf("OpenProcess failed (pid=%d): %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(pg.job, h); err != nil {
		return fmt.Errorf("AssignProcessToJobObject failed (pid=%d): %w", pid, err)
	}
	return nil
}

func (pg *procGroup) close() error {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	if pg.done {
		return nil
	}
	pg.done = true
	if err := windows.CloseHandle(pg.job); err != nil {
		return fmt.Errorf("CloseHandle failed for job object: %w", err)
	}
	pg.job = 0
	return nil
}