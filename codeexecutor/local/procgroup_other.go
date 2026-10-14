//go:build !windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package local

import "os/exec"

type procGroup struct{}

// hideChildWindow 非 Windows 平台为空操作：POSIX 子进程不在控制台里单独弹窗。
func hideChildWindow(_ *exec.Cmd) {}

func newProcGroup() (*procGroup, error) {
	return &procGroup{}, nil
}

func (pg *procGroup) addProcess(pid int) error {
	return nil
}

func (pg *procGroup) close() error {
	return nil
}
