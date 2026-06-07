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
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	lua "github.com/yuin/gopher-lua"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const toolRegistryKey = "luaexec_tool_registry"

// toolReservedNames 是 tool 模块的保留函数名，不允许被工具代理覆盖。
var toolReservedNames = map[string]bool{
	"call":        true,
	"list":        true,
	"declaration": true,
}

// toolRegistry holds the lazily-initialized tool lookup dictionary.
type toolRegistry struct {
	mu           sync.RWMutex
	tools        map[string]tool.CallableTool
	declarations map[string]*tool.Declaration
	names        []string
	initialized  bool
	rawTools     []tool.Tool
	denied       []string
}

func newToolRegistry(tools []tool.Tool, denied []string) *toolRegistry {
	return &toolRegistry{
		rawTools: tools,
		denied:   denied,
	}
}

func (reg *toolRegistry) ensureInitialized() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.initialized {
		return
	}
	reg.buildFromRawTools()
	reg.initialized = true
}

func (reg *toolRegistry) buildFromRawTools() {
	reg.tools = make(map[string]tool.CallableTool, len(reg.rawTools))
	reg.declarations = make(map[string]*tool.Declaration, len(reg.rawTools))
	reg.names = make([]string, 0, len(reg.rawTools))

	// Always deny: lua_exec (prevent recursion), agent tools (prevent sub-agent creation),
	// and sessions_* (aliases of subagents_*, also prevent sub-agent creation).
	alwaysDeny := map[string]bool{
		"lua_exec":         true,
		"luaexec_lua_exec": true,
		"sessions_spawn":   true,
		"sessions_list":    true,
		"sessions_get":     true,
		"sessions_cancel":  true,
	}

	denySet := toSet(reg.denied)
	for k, v := range alwaysDeny {
		denySet[k] = v
	}

	for _, t := range reg.rawTools {
		decl := t.Declaration()
		if decl == nil {
			continue
		}
		name := decl.Name

		// Deny list filter.
		if denySet[name] {
			continue
		}

		// Wildcard rule: exclude tools whose name contains "agent" (prevent sub-agent creation).
		if strings.Contains(strings.ToLower(name), "agent") {
			continue
		}

		// Only register tools that implement CallableTool.
		if ct, ok := t.(tool.CallableTool); ok {
			reg.tools[name] = ct
			reg.declarations[name] = decl
			reg.names = append(reg.names, name)
		}
	}
}

func (reg *toolRegistry) lookup(name string) (tool.CallableTool, error) {
	reg.ensureInitialized()
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	ct, ok := reg.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found or denied, available: %v", name, reg.names)
	}
	return ct, nil
}

func (reg *toolRegistry) listNames() []string {
	reg.ensureInitialized()
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	cp := make([]string, len(reg.names))
	copy(cp, reg.names)
	return cp
}

func (reg *toolRegistry) getDeclaration(name string) (*tool.Declaration, error) {
	reg.ensureInitialized()
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	decl, ok := reg.declarations[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	return decl, nil
}

// registerToolBridge registers the tool module in the Lua VM.
// It creates tool.call/list/declaration functions and dynamically generates
// a proxy function for each registered tool (e.g., tool.fs_read_file({...})).
func registerToolBridge(L *lua.LState, tools []tool.Tool, denied []string) {
	reg := newToolRegistry(tools, denied)
	reg.ensureInitialized()

	mod := L.NewTable()

	// Register built-in functions.
	L.SetField(mod, "call", L.NewFunction(func(L *lua.LState) int {
		return bridgeToolCall(L, reg)
	}))
	L.SetField(mod, "list", L.NewFunction(func(L *lua.LState) int {
		return bridgeToolList(L, reg)
	}))
	L.SetField(mod, "declaration", L.NewFunction(func(L *lua.LState) int {
		return bridgeToolDeclaration(L, reg)
	}))

	// Dynamically generate proxy functions for each registered tool.
	for _, name := range reg.names {
		ident := toolNameToLuaIdent(name)
		// Skip reserved names to prevent overriding built-in functions.
		if toolReservedNames[ident] {
			continue
		}
		toolName := name // Capture for closure.
		L.SetField(mod, ident, L.NewFunction(func(L *lua.LState) int {
			return bridgeToolProxyCall(L, reg, toolName)
		}))
	}

	L.SetGlobal("tool", mod)
}

// toolNameToLuaIdent converts a tool name to a valid Lua identifier.
// Non-alphanumeric/underscore characters are replaced with underscores.
func toolNameToLuaIdent(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// bridgeToolCall implements tool.call(name, args_table).
// Enhanced with safe argument handling and detailed error reporting.
func bridgeToolCall(L *lua.LState, reg *toolRegistry) int {
	// Safe argument handling: return detailed error instead of panic on missing args.
	if L.GetTop() < 1 {
		pushDetailedToolError(L, "", "args_type", "tool.call 缺少工具名参数")
		return 2
	}
	name := L.CheckString(1)

	// Safe argument handling: allow missing second arg (equivalent to empty table {}).
	var jsonArgs []byte
	if L.GetTop() >= 2 {
		arg := L.Get(2)
		if tbl, ok := arg.(*lua.LTable); ok {
			var err error
			jsonArgs, err = luaTableToJSON(tbl)
			if err != nil {
				pushDetailedToolError(L, name, "args_conversion",
					fmt.Sprintf("参数转换失败: %v", err))
				return 2
			}
		} else if arg != lua.LNil {
			pushDetailedToolError(L, name, "args_type",
				fmt.Sprintf("参数类型错误: 期望table, 实际为%s", arg.Type().String()))
			return 2
		}
	}
	if jsonArgs == nil {
		jsonArgs = []byte("{}")
	}

	ct, err := reg.lookup(name)
	if err != nil {
		pushDetailedToolError(L, name, "not_found", err.Error())
		return 2
	}

	// Use the VM's context (inherits lua_exec timeout).
	ctx := L.Context()

	result, err := ct.Call(ctx, jsonArgs)
	if err != nil {
		pushDetailedToolError(L, name, "call_failed", err.Error())
		return 2
	}

	pushGoValue(L, result)
	return 1
}

// bridgeToolProxyCall implements tool.工具名(args_table) proxy call.
// Equivalent to tool.call(name, args_table) but with the tool name already determined.
func bridgeToolProxyCall(L *lua.LState, reg *toolRegistry, name string) int {
	// Safe argument handling: allow no args (equivalent to empty table {}).
	var jsonArgs []byte
	if L.GetTop() >= 1 {
		arg := L.Get(1)
		if tbl, ok := arg.(*lua.LTable); ok {
			var err error
			jsonArgs, err = luaTableToJSON(tbl)
			if err != nil {
				pushDetailedToolError(L, name, "args_conversion",
					fmt.Sprintf("参数转换失败: %v", err))
				return 2
			}
		} else if arg != lua.LNil {
			pushDetailedToolError(L, name, "args_type",
				fmt.Sprintf("参数类型错误: 期望table, 实际为%s", arg.Type().String()))
			return 2
		}
	}
	if jsonArgs == nil {
		jsonArgs = []byte("{}")
	}

	ct, err := reg.lookup(name)
	if err != nil {
		pushDetailedToolError(L, name, "not_found", err.Error())
		return 2
	}

	ctx := L.Context()
	result, err := ct.Call(ctx, jsonArgs)
	if err != nil {
		pushDetailedToolError(L, name, "call_failed", err.Error())
		return 2
	}

	pushGoValue(L, result)
	return 1
}

// bridgeToolList implements tool.list().
func bridgeToolList(L *lua.LState, reg *toolRegistry) int {
	names := reg.listNames()
	tbl := L.NewTable()
	for i, name := range names {
		L.RawSetInt(tbl, i+1, lua.LString(name))
	}
	L.Push(tbl)
	return 1
}

// bridgeToolDeclaration implements tool.declaration(name).
func bridgeToolDeclaration(L *lua.LState, reg *toolRegistry) int {
	name := L.CheckString(1)

	decl, err := reg.getDeclaration(name)
	if err != nil {
		pushBridgeError(L, err.Error())
		return 2
	}

	result := map[string]any{
		"name":        decl.Name,
		"description": decl.Description,
	}
	if decl.InputSchema != nil {
		result["input_schema"] = decl.InputSchema
	}
	if decl.OutputSchema != nil {
		result["output_schema"] = decl.OutputSchema
	}

	pushGoValue(L, result)
	return 1
}

// luaTableToJSON converts a Lua table to JSON bytes.
func luaTableToJSON(tbl *lua.LTable) ([]byte, error) {
	goMap := luaTableToMap(tbl)
	return json.Marshal(goMap)
}

// pushBridgeError pushes a bridge error onto the Lua stack.
func pushBridgeError(L *lua.LState, msg string) {
	L.Push(lua.LNil)
	errTbl := L.NewTable()
	L.SetField(errTbl, "type", lua.LString(ErrTypeBridge))
	L.SetField(errTbl, "message", lua.LString(msg))
	L.Push(errTbl)
}

// pushDetailedToolError pushes a tool_call error with detailed context onto the Lua stack.
// phase identifies the error stage: not_found / args_type / args_conversion / call_failed.
// Lua receives: nil, {type="tool_call", tool="xxx", phase="not_found", message="..."}
func pushDetailedToolError(L *lua.LState, toolName, phase, msg string) {
	L.Push(lua.LNil)
	errTbl := L.NewTable()
	L.SetField(errTbl, "type", lua.LString(ErrTypeToolCall))
	L.SetField(errTbl, "tool", lua.LString(toolName))
	L.SetField(errTbl, "phase", lua.LString(phase))
	L.SetField(errTbl, "message", lua.LString(msg))
	L.Push(errTbl)
}
