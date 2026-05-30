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

type procGroup struct{}

func newProcGroup() (*procGroup, error) {
	return &procGroup{}, nil
}

func (pg *procGroup) addProcess(pid int) error {
	return nil
}

func (pg *procGroup) close() error {
	return nil
}