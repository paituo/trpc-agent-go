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
	"sync"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

var (
	acp     uint32
	acpOnce sync.Once
)

func getACP() uint32 {
	acpOnce.Do(func() {
		acp = windows.GetACP()
	})
	return acp
}

func toUTF8(input []byte) (string, error) {
	if len(input) == 0 {
		return "", nil
	}
	if utf8.Valid(input) {
		return string(input), nil
	}
	acpCode := getACP()
	if acpCode == 65001 {
		return string(input), nil
	}
	wideLen, err := windows.MultiByteToWideChar(
		acpCode, 0, &input[0], int32(len(input)), nil, 0,
	)
	if err != nil {
		return "", err
	}
	if wideLen == 0 {
		return "", nil
	}
	buf := make([]uint16, wideLen)
	_, err = windows.MultiByteToWideChar(
		acpCode, 0, &input[0], int32(len(input)), &buf[0], wideLen,
	)
	if err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf), nil
}