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
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// newState creates a new GopherLua VM with sandbox configuration.
func newState(cfg *Config, callerCtx context.Context) (*lua.LState, context.CancelFunc, *LogCollector) {
	timeout := time.Duration(cfg.DefaultTimeout) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	ctx, cancel := context.WithTimeout(callerCtx, timeout)

	callStackSize := cfg.CallStackSize
	if callStackSize <= 0 {
		callStackSize = 512
	}
	registrySize := cfg.RegistrySize
	if registrySize <= 0 {
		registrySize = 1024 * 20
	}

	opts := lua.Options{
		CallStackSize: callStackSize,
		RegistrySize:  registrySize,
		SkipOpenLibs:  true,
	}
	if opts.RegistryMaxSize <= 0 {
		opts.RegistryMaxSize = 1024 * 80
	}
	if opts.RegistryGrowStep <= 0 {
		opts.RegistryGrowStep = 32
	}
	L := lua.NewState(opts)

	// SetContext enables cooperative interruption at opcode boundaries.
	L.SetContext(ctx)

	// Force-close the VM if context is cancelled (handles C-function blocking).
	go func() {
		<-ctx.Done()
		L.Close()
	}()

	// Load safe standard libraries.
	openSafeLibs(L)

	// Load io/os based on sandbox switches (with dangerous functions removed).
	denied := toSet(cfg.DeniedModules)
	if cfg.AllowIOLib && !denied["io"] {
		openFilteredIOLib(L)
	}
	if cfg.AllowOSLib && !denied["os"] {
		openFilteredOSLib(L)
	}

	// Register bridge modules (default all enabled, DeniedModules excludes).
	if !denied["tool"] && len(cfg.Tools) > 0 {
		registerToolBridge(L, cfg.Tools, cfg.DeniedTools)
		L.PreloadModule("tool", preloadGlobal(L, "tool"))
	}
	if !denied["yaml"] {
		registerYAMLBridge(L, cfg.AllowIOLib)
		L.PreloadModule("yaml", preloadGlobal(L, "yaml"))
	}
	if !denied["json"] {
		registerJSONBridge(L)
		L.PreloadModule("json", preloadGlobal(L, "json"))
	}
	if !denied["html"] {
		registerHTMLBridge(L)
		L.PreloadModule("html", preloadGlobal(L, "html"))
	}
	if !denied["md"] {
		registerMDBridge(L)
		L.PreloadModule("md", preloadGlobal(L, "md"))
	}
	if !denied["htmltable"] {
		registerTableBridge(L)
		L.PreloadModule("htmltable", preloadGlobal(L, "htmltable"))
	}
	if !denied["summarize"] {
		registerSummarizeBridge(L)
		L.PreloadModule("summarize", preloadGlobal(L, "summarize"))
	}
	if !denied["utf8"] {
		registerUTF8Bridge(L)
		L.PreloadModule("utf8", preloadGlobal(L, "utf8"))
		L.PreloadModule("re", preloadGlobal(L, "re"))
		// Backward compatibility: if re is explicitly denied, remove re alias
		if denied["re"] {
			L.SetGlobal("re", lua.LNil)
		}
	}

	// Register fs bridge module (controlled by AllowFSLib + DeniedModules).
	if cfg.AllowFSLib && !denied["fs"] {
		registerFSBridge(L, cfg)
		L.PreloadModule("fs", preloadGlobal(L, "fs"))
	}

	// Register kb (knowledge base) module if configured.
	if !denied["kb"] && cfg.KBModule != nil {
		cfg.KBModule.Register(L)
		L.PreloadModule("kb", preloadGlobal(L, "kb"))
	}

	// Register ordered_table type (always enabled).
	if !denied["ordered_table"] {
		registerOrderedTableType(L)
	}

	// Register log bridge module (always enabled, debug level gated by EnableDebug).
	if !denied["log"] {
		lc := newLogCollector(cfg.MaxLogEntries, cfg.EnableDebug)
		registerLogBridge(L, lc)
		L.PreloadModule("log", preloadGlobal(L, "log"))
		return L, cancel, lc
	}

	return L, cancel, nil
}

// openSafeLibs loads the safe standard libraries: base, package, string, table, math.
func openSafeLibs(L *lua.LState) {
	for _, lib := range []struct {
		name string
		open lua.LGFunction
	}{
		{lua.LoadLibName, lua.OpenPackage},
		{lua.BaseLibName, lua.OpenBase},
		{lua.StringLibName, lua.OpenString},
		{lua.TabLibName, lua.OpenTable},
		{lua.MathLibName, lua.OpenMath},
	} {
		L.Push(L.NewFunction(lib.open))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}
}

// openFilteredIOLib loads the io library with popen removed.
func openFilteredIOLib(L *lua.LState) {
	L.Push(L.NewFunction(lua.OpenIo))
	L.Push(lua.LString(lua.IoLibName))
	L.Call(1, 0)

	// Remove io.popen to prevent command injection.
	mod := L.GetGlobal("io")
	if tbl, ok := mod.(*lua.LTable); ok {
		tbl.RawSetString("popen", lua.LNil)
	}
}

// openFilteredOSLib loads the os library with dangerous functions removed.
func openFilteredOSLib(L *lua.LState) {
	L.Push(L.NewFunction(lua.OpenOs))
	L.Push(lua.LString(lua.OsLibName))
	L.Call(1, 0)

	// Remove dangerous os functions.
	mod := L.GetGlobal("os")
	if tbl, ok := mod.(*lua.LTable); ok {
		for _, name := range []string{"execute", "getenv", "exit", "remove", "rename", "tmpname"} {
			tbl.RawSetString(name, lua.LNil)
		}
	}
}

// preloadGlobal returns a loader function that pushes the global value with the given name.
// This allows bridge modules registered via SetGlobal to also be loaded via require().
func preloadGlobal(L *lua.LState, name string) lua.LGFunction {
	return func(L *lua.LState) int {
		L.Push(L.GetGlobal(name))
		return 1
	}
}

// toSet converts a string slice to a set for fast lookup.
func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[strings.ToLower(item)] = true
	}
	return s
}

// containsString checks if a string is in a slice (case-insensitive).
func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, target) {
			return true
		}
	}
	return false
}
