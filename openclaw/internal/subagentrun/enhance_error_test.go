//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package subagentrun

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEnhanceWaitError_NilError(t *testing.T) {
	ctx := context.Background()
	err := enhanceWaitError(ctx, nil, "test-run", "sync", 600, 0)
	assert.Nil(t, err)
}

func TestEnhanceWaitError_NonContextError(t *testing.T) {
	ctx := context.Background()
	originalErr := errors.New("some other error")
	err := enhanceWaitError(ctx, originalErr, "test-run", "sync", 600, 0)
	assert.Equal(t, originalErr, err)
}

func TestEnhanceWaitError_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := enhanceWaitError(ctx, context.Canceled, "test-run-123", "sync", 600, 0)

	assert.NotNil(t, err)
	assert.True(t, errors.Is(err, context.Canceled))

	errMsg := err.Error()
	assert.Contains(t, errMsg, "subagent wait failed: context canceled")
	assert.Contains(t, errMsg, "subagent run ID: test-run-123")
	assert.Contains(t, errMsg, "spawn mode: sync")
	assert.Contains(t, errMsg, "subagent timeout: 600s")
	assert.Contains(t, errMsg, "wait timeout: inherited from parent context")
	assert.Contains(t, errMsg, "parent context status: canceled")
	assert.Contains(t, errMsg, "set wait_timeout_seconds to create an independent timeout context")
}

func TestEnhanceWaitError_DeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Nanosecond) // Wait for timeout

	err := enhanceWaitError(ctx, context.DeadlineExceeded, "test-run-456", "review", 300, 400)

	assert.NotNil(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded))

	errMsg := err.Error()
	assert.Contains(t, errMsg, "subagent wait failed: timeout exceeded")
	assert.Contains(t, errMsg, "subagent run ID: test-run-456")
	assert.Contains(t, errMsg, "spawn mode: review")
	assert.Contains(t, errMsg, "subagent timeout: 300s")
	assert.Contains(t, errMsg, "wait timeout: 400s")
}

func TestEnhanceWaitError_WithWaitTimeout(t *testing.T) {
	ctx := context.Background()

	err := enhanceWaitError(ctx, context.Canceled, "test-run-789", "sync", 600, 700)

	assert.NotNil(t, err)
	errMsg := err.Error()
	assert.Contains(t, errMsg, "wait timeout: 700s")
	// Should not suggest setting wait_timeout_seconds since it's already set
	assert.NotContains(t, errMsg, "set wait_timeout_seconds to create an independent timeout context")
}

func TestEnhanceWaitError_TimeoutMisconfiguration(t *testing.T) {
	ctx := context.Background()

	// Test case: wait_timeout_seconds <= timeout_seconds (misconfiguration)
	err := enhanceWaitError(ctx, context.Canceled, "test-run", "sync", 600, 500)

	assert.NotNil(t, err)
	errMsg := err.Error()
	assert.Contains(t, errMsg, "consider setting wait_timeout_seconds > timeout_seconds")
}

func TestEnhanceWaitError_ActiveParentContext(t *testing.T) {
	ctx := context.Background()

	err := enhanceWaitError(ctx, context.Canceled, "test-run", "sync", 600, 0)

	assert.NotNil(t, err)
	errMsg := err.Error()
	assert.Contains(t, errMsg, "parent context status: active")
}

func TestEnhanceWaitError_DefaultTimeout(t *testing.T) {
	ctx := context.Background()

	// Test case: no timeout configured (default)
	err := enhanceWaitError(ctx, context.Canceled, "test-run", "sync", 0, 0)

	assert.NotNil(t, err)
	errMsg := err.Error()
	assert.Contains(t, errMsg, "subagent timeout: default (unlimited)")
}
