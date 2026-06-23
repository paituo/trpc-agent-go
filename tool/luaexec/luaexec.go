//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package luaexec

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Option configures the luaexec tool set.
type Option func(*Config)

// WithName sets the tool set name.
func WithName(name string) Option {
	return func(c *Config) { c.Name = name }
}

// WithDefaultTimeout sets the default script timeout in seconds.
func WithDefaultTimeout(sec int) Option {
	return func(c *Config) { c.DefaultTimeout = sec }
}

// WithDeniedModules sets the list of disabled bridge modules.
func WithDeniedModules(modules ...string) Option {
	return func(c *Config) { c.DeniedModules = modules }
}

// WithAllowIOLib controls whether the Lua io standard library is available.
func WithAllowIOLib(allow bool) Option {
	return func(c *Config) { c.AllowIOLib = allow }
}

// WithCallStackSize sets the Lua call stack size. Defaults to 256.
func WithCallStackSize(n int) Option {
	return func(c *Config) { c.CallStackSize = n }
}

// WithRegistrySize sets the Lua registry size. Defaults to 512.
func WithRegistrySize(n int) Option {
	return func(c *Config) { c.RegistrySize = n }
}

// WithAllowOSLib controls whether the Lua os standard library is available.
func WithAllowOSLib(allow bool) Option {
	return func(c *Config) { c.AllowOSLib = allow }
}

// WithAllowFSLib controls whether the fs bridge module is available in Lua scripts.
// When enabled, fs.read_file, fs.write_file, fs.list_dir, etc. can be used
// for controlled filesystem access within allowed_script_dirs.
// Defaults to true.
func WithAllowFSLib(allow bool) Option {
	return func(c *Config) { c.AllowFSLib = allow }
}

// WithTools sets the list of registered tools available to Lua scripts.
func WithTools(tools ...tool.Tool) Option {
	return func(c *Config) { c.Tools = tools }
}

// WithDeniedTools sets the list of tool names that Lua scripts must not call.
func WithDeniedTools(tools ...string) Option {
	return func(c *Config) { c.DeniedTools = tools }
}

// WithAllowedScriptDirs sets the directories from which script_path can load
// Lua scripts. If empty (default), script_path is disabled for security.
func WithAllowedScriptDirs(dirs ...string) Option {
	return func(c *Config) { c.AllowedScriptDirs = dirs }
}

// WithToolsProvider sets a dynamic tool provider function.
// When set, Call() resolves it before creating the VM.
// Takes precedence over WithTools.
func WithToolsProvider(provider ToolsProvider) Option {
	return func(c *Config) { c.ToolsProvider = provider }
}

// WithMaxOutputLen sets the maximum length of Lua print() and return value output.
func WithMaxOutputLen(n int) Option {
	return func(c *Config) { c.MaxOutputLen = n }
}

// WithMaxErrorLen sets the maximum length of a single error message.
func WithMaxErrorLen(n int) Option {
	return func(c *Config) { c.MaxErrorLen = n }
}

// WithEnableDebug controls whether log.debug() outputs are collected.
func WithEnableDebug(enable bool) Option {
	return func(c *Config) { c.EnableDebug = enable }
}

// WithMaxLogEntries sets the maximum number of log entries per execution.
func WithMaxLogEntries(n int) Option {
	return func(c *Config) { c.MaxLogEntries = n }
}

// NewToolSet creates a Lua script execution tool set.
func NewToolSet(opts ...Option) (tool.ToolSet, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	// Validate: tool module needs at least one tool source unless explicitly disabled.
	hasToolSource := len(cfg.Tools) > 0 || cfg.ToolsProvider != nil
	if !hasToolSource && !containsString(cfg.DeniedModules, "tool") {
		return nil, fmt.Errorf("tool module enabled but no tool source provided (use WithTools, WithToolsProvider, or WithDeniedModules(\"tool\"))")
	}

	return &toolSet{cfg: cfg}, nil
}

type toolSet struct {
	cfg Config
}

var _ tool.ToolSet = (*toolSet)(nil)

func (ts *toolSet) Name() string { return ts.cfg.Name }

func (ts *toolSet) Close() error { return nil }

func (ts *toolSet) Tools(_ context.Context) []tool.Tool {
	return []tool.Tool{&luaExecTool{cfg: ts.cfg}}
}
