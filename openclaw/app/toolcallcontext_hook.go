//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
)

// SetToolCallContextHook sets the hook for tool_call_context processing.
// This is a public wrapper around gateway.SetToolCallContextHook that allows
// plugins to register hooks for tool_call_context processing (e.g., for
// confirm_form write-back).
func SetToolCallContextHook(hook func(ctx context.Context, sessionID string, items []gwproto.ToolCallContextItem)) {
	gateway.SetToolCallContextHook(hook)
}
