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
	"reflect"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// fmtS is a shorthand for fmt.Sprintf.
func fmtS(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// marshalStructFull 将任意 Go 值序列化为 JSON，对实现了 json.Marshaler
// 的类型绕过自定义 MarshalJSON()，保留结构体完整字段。
// 这确保 MCP 等工具返回的结构体在 Lua 端不会丢失 Meta/IsError 等字段。
//
// 与标准 json.Marshal 的差异：
//   - 绕过自定义 MarshalJSON()，直接序列化结构体原始字段
//   - 保留 json:"-" 标记的字段（Lua 端需要最大信息量）
//   - 使用 JSON tag 中的名称（如有），omitempty 等选项被忽略
func marshalStructFull(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	// 解引用指针/接口
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return json.Marshal(nil)
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		// 非结构体，直接用标准 json.Marshal
		return json.Marshal(v)
	}
	// 结构体：遍历导出字段构建 map
	m := make(map[string]any)
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}
		// 跳过 nil 值字段（防御性检查）
		fv := rv.Field(i)
		if fv.Kind() == reflect.Interface || fv.Kind() == reflect.Ptr || fv.Kind() == reflect.Slice || fv.Kind() == reflect.Map {
			if fv.IsNil() {
				continue
			}
		}
		// 使用 JSON tag 中的名称（如有）。
		// 注意：json:"-" 字段保留（Lua 端需要最大信息量），
		// 仅使用 tag 中的名称映射，不跳过任何导出字段。
		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" {
			parts := strings.Split(tag, ",")
			// parts[0] 非空且非 "-" 时使用 tag 名称
			// parts[0] 为空（如 json:",omitempty"）时保持 field.Name
			// json:"-" 时保持 field.Name（不跳过）
			if parts[0] != "" && parts[0] != "-" {
				name = parts[0]
			}
		}
		m[name] = fv.Interface()
	}
	return json.Marshal(m)
}

// pushGoValue converts a Go value to a Lua value and pushes it onto the stack.
func pushGoValue(L *lua.LState, v any) {
	if v == nil {
		L.Push(lua.LNil)
		return
	}
	switch val := v.(type) {
	case bool:
		L.Push(lua.LBool(val))
	case int:
		L.Push(lua.LNumber(val))
	case int64:
		L.Push(lua.LNumber(val))
	case float64:
		L.Push(lua.LNumber(val))
	case string:
		L.Push(lua.LString(val))
	case map[string]any:
		tbl := L.NewTable()
		for k, elem := range val {
			L.SetField(tbl, k, toLValue(L, elem))
		}
		L.Push(tbl)
	case []any:
		tbl := L.NewTable()
		for i, elem := range val {
			L.RawSetInt(tbl, i+1, toLValue(L, elem))
		}
		L.Push(tbl)
	default:
		// 通过 JSON 中转将任意 Go 值转为 map[string]any。
		// 使用 marshalStructFull 绕过自定义 MarshalJSON()，
		// 保留 MCP 等工具返回值的完整结构。
		jsonBytes, err := marshalStructFull(v)
		if err != nil {
			L.Push(lua.LString(fmtS("%v", v)))
			return
		}
		var mapped any
		if err := json.Unmarshal(jsonBytes, &mapped); err != nil {
			L.Push(lua.LString(string(jsonBytes)))
			return
		}
		pushGoValue(L, mapped)
	}
}

// toLValue converts a Go value to an LValue.
func toLValue(L *lua.LState, v any) lua.LValue {
	if v == nil {
		return lua.LNil
	}
	switch val := v.(type) {
	case bool:
		return lua.LBool(val)
	case int:
		return lua.LNumber(val)
	case int64:
		return lua.LNumber(val)
	case float64:
		return lua.LNumber(val)
	case string:
		return lua.LString(val)
	case map[string]any:
		tbl := L.NewTable()
		for k, elem := range val {
			L.SetField(tbl, k, toLValue(L, elem))
		}
		return tbl
	case []any:
		tbl := L.NewTable()
		for i, elem := range val {
			L.RawSetInt(tbl, i+1, toLValue(L, elem))
		}
		return tbl
	default:
		jsonBytes, err := marshalStructFull(v)
		if err != nil {
			return lua.LString(fmtS("%v", v))
		}
		var mapped any
		if err := json.Unmarshal(jsonBytes, &mapped); err != nil {
			return lua.LString(string(jsonBytes))
		}
		return toLValue(L, mapped)
	}
}

// luaTableToMap converts a Lua table to map[string]any.
func luaTableToMap(tbl *lua.LTable) map[string]any {
	result := make(map[string]any)
	tbl.ForEach(func(k, v lua.LValue) {
		key := luaValueToString(k)
		result[key] = lValueToGo(v)
	})
	return result
}

// lValueToGo converts an LValue to a Go value.
func lValueToGo(v lua.LValue) any {
	switch val := v.(type) {
	case lua.LBool:
		return bool(val)
	case lua.LNumber:
		return float64(val)
	case lua.LString:
		return string(val)
	case *lua.LTable:
		// Check if it looks like an array (all keys are integers starting from 1)
		if isArrayTable(val) {
			return lTableToSlice(val)
		}
		return luaTableToMap(val)
	case *lua.LNilType:
		return nil
	default:
		return v.String()
	}
}

// isArrayTable checks if a Lua table is array-like.
func isArrayTable(tbl *lua.LTable) bool {
	arr := tbl.MaxN()
	if arr == 0 {
		return false
	}
	// If MaxN > 0 and there are no string keys, treat as array
	hasStrKey := false
	tbl.ForEach(func(k, _ lua.LValue) {
		if k.Type() != lua.LTNumber {
			hasStrKey = true
		}
	})
	return !hasStrKey
}

// lTableToSlice converts an array-like Lua table to []any.
func lTableToSlice(tbl *lua.LTable) []any {
	n := tbl.MaxN()
	if n == 0 {
		return nil
	}
	result := make([]any, n)
	for i := 1; i <= n; i++ {
		result[i-1] = lValueToGo(tbl.RawGetInt(i))
	}
	return result
}

// luaValueToString converts an LValue to a string key.
func luaValueToString(v lua.LValue) string {
	switch val := v.(type) {
	case lua.LString:
		return string(val)
	case lua.LNumber:
		return fmtS("%v", float64(val))
	default:
		return v.String()
	}
}
