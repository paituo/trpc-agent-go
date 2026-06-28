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
	"fmt"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

const logCollectorKey = "luaexec_log_collector"

// LogEntry represents a single log entry from the Lua log module.
type LogEntry struct {
	Level     string `json:"level"`
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
}

// LogCollector collects log entries produced by the Lua log module.
type LogCollector struct {
	mu          sync.Mutex
	entries     []LogEntry
	maxEntries  int
	enableDebug bool
}

func newLogCollector(maxEntries int, enableDebug bool) *LogCollector {
	if maxEntries <= 0 {
		maxEntries = 500
	}
	return &LogCollector{
		entries:     make([]LogEntry, 0, 32),
		maxEntries:  maxEntries,
		enableDebug: enableDebug,
	}
}

func (lc *LogCollector) add(level, message string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if len(lc.entries) >= lc.maxEntries {
		return
	}
	lc.entries = append(lc.entries, LogEntry{
		Level:     level,
		Timestamp: time.Now().Format(time.RFC3339),
		Message:   message,
	})
}

func (lc *LogCollector) collect() []LogEntry {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if len(lc.entries) == 0 {
		return nil
	}
	cp := make([]LogEntry, len(lc.entries))
	copy(cp, lc.entries)
	return cp
}

// getLogCollector retrieves the LogCollector from the Lua state registry.
func getLogCollector(L *lua.LState) *LogCollector {
	v := L.GetField(L.Get(lua.RegistryIndex), logCollectorKey)
	if ud, ok := v.(*lua.LUserData); ok {
		if lc, ok := ud.Value.(*LogCollector); ok {
			return lc
		}
	}
	return nil
}

// registerLogBridge registers the log module in the Lua VM.
// Provides log.info/warn/error/debug functions for structured logging.
func registerLogBridge(L *lua.LState, lc *LogCollector) {
	// Store collector in registry so bridge functions can access it.
	ud := L.NewUserData()
	ud.Value = lc
	L.SetField(L.Get(lua.RegistryIndex), logCollectorKey, ud)

	mod := L.NewTable()

	levels := []struct {
		name  string
		level string
	}{
		{"info", "info"},
		{"warn", "warn"},
		{"error", "error"},
		{"debug", "debug"},
	}

	for _, lv := range levels {
		lv := lv // capture for closure
		L.SetField(mod, lv.name, L.NewFunction(func(L *lua.LState) int {
			// debug level is gated by EnableDebug config.
			if lv.level == "debug" && !lc.enableDebug {
				return 0
			}
			top := L.GetTop()
			var parts []string
			for i := 1; i <= top; i++ {
				parts = append(parts, L.ToStringMeta(L.Get(i)).String())
			}
			msg := fmt.Sprint(partsToInterface(parts...)...)
			// Collect log entry for diagnostics.
			lc.add(lv.level, msg)
			// Also output to trpc-agent-go project log system.
			outputToProjectLog(lv.level, msg)
			return 0
		}))
	}

	L.SetGlobal("log", mod)
}

// partsToInterface converts string parts to []any for fmt.Sprint.
func partsToInterface(parts ...string) []any {
	result := make([]any, len(parts))
	for i, p := range parts {
		result[i] = p
	}
	return result
}

// outputToProjectLog outputs a log message to the trpc-agent-go project log system.
// It maps Lua log levels to the corresponding project log functions.
func outputToProjectLog(level, message string) {
	switch level {
	case "debug":
		log.Debug("[luaexec] ", message)
	case "info":
		log.Info("[luaexec] ", message)
	case "warn":
		log.Warn("[luaexec] ", message)
	case "error":
		log.Error("[luaexec] ", message)
	default:
		log.Info("[luaexec] ", message)
	}
}
