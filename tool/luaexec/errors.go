//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package luaexec

// LuaError represents a structured error from Lua script execution.
type LuaError struct {
	Line    int    `json:"line,omitempty"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Error type constants.
const (
	ErrTypeSyntax   = "syntax"
	ErrTypeRuntime  = "runtime"
	ErrTypeTimeout  = "timeout"
	ErrTypeToolCall = "tool_call"
	ErrTypeBridge   = "bridge"
	ErrTypeEncoding = "encoding"
)

// truncateMessage truncates msg to maxLen bytes, appending "..." if truncated.
func truncateMessage(msg string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 1024
	}
	if len(msg) <= maxLen {
		return msg
	}
	if maxLen <= 3 {
		return msg[:maxLen]
	}
	return msg[:maxLen-3] + "..."
}
