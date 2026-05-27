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

package local

// localJobObject is a stub for non-Windows platforms.
type localJobObject struct{}

func newLocalJobObject() (*localJobObject, error) { return nil, nil }

func (j *localJobObject) enableKillOnClose() error { return nil }

func (j *localJobObject) assignProcess(_ any) error { return nil }

func (j *localJobObject) close() error { return nil }