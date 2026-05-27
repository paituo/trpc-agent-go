//go:build !windows

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

// jobObject is a no-op stub for non-Windows platforms.
// On Windows, procgroup_windows.go provides the real implementation.
type jobObject struct{}

// newJobObject is a stub for non-Windows platforms.
func newJobObject() (*jobObject, error) {
	return nil, nil
}

// close is a stub for non-Windows platforms.
func (j *jobObject) close() error {
	return nil
}

// enableKillOnClose is a stub for non-Windows platforms.
func (j *jobObject) enableKillOnClose() error {
	return nil
}

// assignProcess is a stub for non-Windows platforms.
func (j *jobObject) assignProcess(_ any) error {
	return nil
}