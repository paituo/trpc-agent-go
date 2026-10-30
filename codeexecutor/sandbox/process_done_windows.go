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

package sandbox

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// isProcessDoneError reports whether err indicates the process is gone.
// After the process handle is released or the process has exited,
// termination attempts fail with EINVAL (the pid no longer refers to a
// live process), ERROR_INVALID_PARAMETER, or ERROR_ACCESS_DENIED
// instead of reporting a dead process; on Windows those are the
// equivalent of ESRCH on Unix.
func isProcessDoneError(err error) bool {
	return errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
