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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/luaexec"
)

// This file keeps the locally added lua and agent_tool providers out of
// tooling_builtins.go, so upstream additions to the built-in provider registry
// do not collide with local ones. Registration happens through init() below,
// which runs alongside the built-in registrations.

const (
	toolSetProviderLua       = "lua"
	toolSetProviderAgentTool = "agent_tool"
)

func init() {
	must(registry.RegisterToolProvider(
		toolSetProviderLua,
		newLuaExecToolProvider,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderAgentTool,
		newAgentToolSet,
	))
}

// --- Lua tool set provider ---

type luaToolSetConfig struct {
	DefaultTimeout     int      `yaml:"default_timeout,omitempty"`
	MaxOutputLen       int      `yaml:"max_output_len,omitempty"`
	MaxErrorLen        int      `yaml:"max_error_len,omitempty"`
	DeniedModules      []string `yaml:"denied_modules,omitempty"`
	AllowIOLib         *bool    `yaml:"allow_io_lib,omitempty"`
	AllowOSLib         *bool    `yaml:"allow_os_lib,omitempty"`
	AllowFSLib         *bool    `yaml:"allow_fs_lib,omitempty"`
	DeniedTools        []string `yaml:"denied_tools,omitempty"`
	AllowedScriptDirs  []string `yaml:"allowed_script_dirs,omitempty"`
	AddSkillScriptDirs *bool    `yaml:"add_skill_script_dirs,omitempty"`
	EnableDebug        *bool    `yaml:"enable_debug,omitempty"`
	MaxLogEntries      int      `yaml:"max_log_entries,omitempty"`
}

func newLuaExecToolProvider(
	deps registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg luaToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	opts := make([]luaexec.Option, 0, 8)
	if name := strings.TrimSpace(spec.Name); name != "" {
		opts = append(opts, luaexec.WithName(name))
	}
	if cfg.DefaultTimeout > 0 {
		opts = append(opts, luaexec.WithDefaultTimeout(cfg.DefaultTimeout))
	}
	if cfg.MaxOutputLen > 0 {
		opts = append(opts, luaexec.WithMaxOutputLen(cfg.MaxOutputLen))
	}
	if cfg.MaxErrorLen > 0 {
		opts = append(opts, luaexec.WithMaxErrorLen(cfg.MaxErrorLen))
	}
	if len(cfg.DeniedModules) > 0 {
		opts = append(opts, luaexec.WithDeniedModules(cfg.DeniedModules...))
	}
	if cfg.AllowIOLib != nil {
		opts = append(opts, luaexec.WithAllowIOLib(*cfg.AllowIOLib))
	}
	if cfg.AllowOSLib != nil {
		opts = append(opts, luaexec.WithAllowOSLib(*cfg.AllowOSLib))
	}
	if cfg.AllowFSLib != nil {
		opts = append(opts, luaexec.WithAllowFSLib(*cfg.AllowFSLib))
	}
	if len(cfg.DeniedTools) > 0 {
		opts = append(opts, luaexec.WithDeniedTools(cfg.DeniedTools...))
	}
	if cfg.EnableDebug != nil {
		opts = append(opts, luaexec.WithEnableDebug(*cfg.EnableDebug))
	}
	if cfg.MaxLogEntries > 0 {
		opts = append(opts, luaexec.WithMaxLogEntries(cfg.MaxLogEntries))
	}

	// Build the final allowed_script_dirs list.
	// When add_skill_script_dirs is true (default), automatically append
	// all skill roots to the explicitly configured allowed_script_dirs.
	addSkillDirs := true // default
	if cfg.AddSkillScriptDirs != nil {
		addSkillDirs = *cfg.AddSkillScriptDirs
	}

	scriptDirs := make([]string, 0, len(cfg.AllowedScriptDirs)+len(deps.SkillsRoots))
	scriptDirs = append(scriptDirs, cfg.AllowedScriptDirs...)
	if addSkillDirs {
		scriptDirs = append(scriptDirs, deps.SkillsRoots...)
	}

	if len(scriptDirs) > 0 {
		opts = append(opts, luaexec.WithAllowedScriptDirs(scriptDirs...))
	}

	// Use ToolsProvider to dynamically obtain the tool list from
	// InvocationContext at runtime, solving the chicken-and-egg problem
	// where the full tool list is not yet available at ToolSet creation time.
	opts = append(opts, luaexec.WithToolsProvider(toolsFromInvocationContext))

	// Pass KB embedder and reranker config to luaexec for kb module initialization.
	if deps.KBEmbedder != nil {
		opts = append(opts, luaexec.WithKBConfig(luaexec.KBModuleConfig{
			EmbedderBaseURL: deps.KBEmbedder.BaseURL,
			EmbedderModel:   deps.KBEmbedder.Model,
			EmbedderAPIKey:  deps.KBEmbedder.APIKey,
			Dimensions:      deps.KBEmbedder.Dimensions,
			RerankerType:    deps.KBEmbedder.RerankerType,
			RerankerModel:   deps.KBEmbedder.RerankerModel,
			RerankerURL:     deps.KBEmbedder.RerankerURL,
			RerankerAPIKey:  deps.KBEmbedder.RerankerAPIKey,
			RerankerTopN:    deps.KBEmbedder.RerankerTopN,
		}))
	}

	t, err := luaexec.NewTool(opts...)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{t}, nil
}

// toolsFromInvocationContext obtains the tool list from the GopherLua VM's context.
//
// Call chain:
//
//	L.Context() → agent.InvocationFromContext(ctx) → inv.Agent.Tools()
//
// GopherLua's L.Context() inherits the ctx parameter from lua_exec Call(),
// which is wrapped by FunctionCallResponseProcessor via agent.NewInvocationContext,
// so agent.InvocationFromContext can retrieve the Invocation,
// and then Invocation.Agent.Tools() returns the current Agent's tool list.
func toolsFromInvocationContext(ctx context.Context) []tool.Tool {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Agent == nil {
		return nil
	}
	return inv.Agent.Tools()
}

// --- Agent tool set provider ---

// agentToolSetConfig maps the YAML "tools.toolsets[].config" section for agent_tool.
type agentToolSetConfig struct {
	Enabled             bool     `yaml:"enabled,omitempty"`
	HistoryScope        string   `yaml:"history_scope,omitempty"`
	StreamInner         bool     `yaml:"stream_inner,omitempty"`
	ExposeToolSelection bool     `yaml:"expose_tool_selection,omitempty"`
	ExposeInstruction   bool     `yaml:"expose_instruction,omitempty"`
	ExcludeTools        []string `yaml:"exclude_tools,omitempty"`
}

func (c *agentToolSetConfig) toHistoryScope() agenttool.HistoryScope {
	switch strings.ToLower(strings.TrimSpace(c.HistoryScope)) {
	case "parent_branch":
		return agenttool.HistoryScopeParentBranch
	default:
		return agenttool.HistoryScopeIsolated
	}
}

// filterByBlacklist filters tools by excluding those whose names match the blacklist.
func filterByBlacklist(tools []tool.Tool, blacklist []string) []tool.Tool {
	if len(blacklist) == 0 {
		return tools
	}
	exclude := make(map[string]bool, len(blacklist))
	for _, name := range blacklist {
		exclude[name] = true
	}
	out := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		if !exclude[t.Declaration().Name] {
			out = append(out, t)
		}
	}
	return out
}

func newAgentToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg agentToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, nil
	}

	// Use CapabilitySurfaceProvider to dynamically obtain the tool list
	// from the parent invocation at call time, solving the chicken-and-egg
	// problem where the full tool list is not yet available at ToolSet
	// creation time. This mirrors the Lua tool's toolsFromInvocationContext pattern.
	provider := func(ctx context.Context, parentInv *agent.Invocation) ([]tool.Tool, map[string]bool) {
		if parentInv == nil || parentInv.Agent == nil {
			return nil, nil
		}
		allTools := parentInv.Agent.Tools()
		return filterByBlacklist(allTools, cfg.ExcludeTools), nil
	}

	agentTool := agenttool.NewDynamicTool(
		agenttool.WithCapabilityProvider(provider),
		agenttool.WithHistoryScope(cfg.toHistoryScope()),
		agenttool.WithStreamInner(cfg.StreamInner),
		agenttool.WithExposeToolSelection(cfg.ExposeToolSelection),
		agenttool.WithExposeInstruction(cfg.ExposeInstruction),
	)

	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = toolSetProviderAgentTool
	}
	return &singleToolToolSet{tool: agentTool, name: name}, nil
}

// singleToolToolSet wraps a single tool.Tool as a tool.ToolSet.
type singleToolToolSet struct {
	tool tool.Tool
	name string
}

func (s *singleToolToolSet) Tools(_ context.Context) []tool.Tool {
	if s == nil || s.tool == nil {
		return nil
	}
	return []tool.Tool{s.tool}
}

func (s *singleToolToolSet) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

func (s *singleToolToolSet) Close() error { return nil }
