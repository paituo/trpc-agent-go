//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package luaexec provides a GopherLua-based script execution tool
// for batch data validation, structured processing, and one-shot
// multi-tool orchestration within the agent system.
package luaexec

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const defaultToolSetName = "luaexec"

// ToolsProvider returns the current tool list given a context.
// The context may carry an Invocation (via agent.InvocationFromContext),
// from which the Agent's tool list can be obtained.
// This allows luaexec to obtain tools lazily at runtime rather than
// at creation time, solving the chicken-and-egg problem where the
// full tool list is not yet available when the ToolSet is created.
type ToolsProvider func(ctx context.Context) []tool.Tool

// Config controls the behavior of the luaexec tool set.
type Config struct {
	// Name is the tool set name. Defaults to "luaexec".
	Name string

	// DefaultTimeout is the default script execution timeout in seconds.
	// Defaults to 300 (5 minutes) because some tasks take a long time.
	DefaultTimeout int

	// MaxOutputLen is the maximum length of Lua print() and return value
	// output. Defaults to 65536.
	MaxOutputLen int

	// MaxErrorLen is the maximum length of a single error message.
	// Defaults to 1024 to control context window usage.
	MaxErrorLen int

	// DeniedModules lists bridge modules to disable.
	// Available: "tool", "yaml", "json", "utf8", "html", "md", "summarize", "log", "io", "os".
	// Note: "re" is now an alias for "utf8"; use "utf8" to disable.
	// Default: empty (all enabled).
	DeniedModules []string

	// CallStackSize controls the Lua call stack size.
	// Defaults to 256. Increase for deeply recursive scripts.
	CallStackSize int

	// RegistrySize controls the Lua registry size.
	// Defaults to 512. Increase for scripts with many tables/closures.
	// On an 8GB machine, 65536 is safe (~256MB peak).
	RegistrySize int

	// AllowIOLib controls whether the Lua io standard library is available.
	// Defaults to false. When true, io.open etc. can be used for file I/O.
	AllowIOLib bool

	// AllowOSLib controls whether the Lua os standard library is available.
	// Defaults to false. When true, os.time/os.date etc. can be used.
	// Dangerous functions (execute/getenv/exit/remove/rename/tmpname) are
	// always removed.
	AllowOSLib bool

	// Tools is the list of registered tools passed by openclaw at init time.
	// luaexec lazily builds an internal dictionary on first tool.call().
	Tools []tool.Tool

	// DeniedTools lists tool names that Lua scripts must not call.
	// lua_exec and agent-related tools are always denied automatically.
	DeniedTools []string

	// AllowedScriptDirs lists directories from which script_path can load
	// Lua scripts. If empty, script_path is disabled. Paths are resolved
	// to absolute paths and must be under one of these directories.
	AllowedScriptDirs []string

	// ToolsProvider returns the current tool list at runtime.
	// When non-nil, Call() resolves it before creating the VM and
	// merges the result into Tools. Takes precedence over static Tools.
	// Used by OpenClaw integration to obtain tools dynamically
	// from the Agent via InvocationContext.
	ToolsProvider ToolsProvider

	// EnableDebug controls whether log.debug() outputs are collected.
	// Defaults to false. When false, log.debug() calls are silently ignored.
	EnableDebug bool

	// MaxLogEntries is the maximum number of log entries collected per execution.
	// Defaults to 500. Excess entries are silently dropped.
	MaxLogEntries int
}

func defaultConfig() Config {
	return Config{
		Name:           defaultToolSetName,
		DefaultTimeout: 300,
		MaxOutputLen:   65536,
		MaxErrorLen:    1024,
		AllowIOLib:     false,
		AllowOSLib:     false,
	}
}
