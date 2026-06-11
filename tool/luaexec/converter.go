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
	"sort"
	"strings"
	"unicode/utf8"

	lua "github.com/yuin/gopher-lua"
	"gopkg.in/yaml.v3"
)

// fmtS is a shorthand for fmt.Sprintf.
func fmtS(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// orderedMapItem represents a key-value pair in an ordered map.
type orderedMapItem struct {
	Key   string
	Value any
}

// orderedMap is a map that preserves insertion order and implements yaml.Marshaler
// for ordered YAML serialization.
type orderedMap struct {
	items []orderedMapItem
}

// MarshalYAML implements yaml.Marshaler for ordered YAML output.
func (om *orderedMap) MarshalYAML() (any, error) {
	// Convert to yaml.Node for ordered output
	var content []*yaml.Node
	for _, item := range om.items {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: item.Key}
		valNode, err := anyToYAMLNode(item.Value)
		if err != nil {
			return nil, err
		}
		content = append(content, keyNode, valNode)
	}
	return &yaml.Node{
		Kind:    yaml.MappingNode,
		Content: content,
	}, nil
}

// anyToYAMLNode converts a Go value to a yaml.Node.
func anyToYAMLNode(v any) (*yaml.Node, error) {
	if v == nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Value: "null"}, nil
	}
	switch val := v.(type) {
	case bool:
		n := &yaml.Node{Kind: yaml.ScalarNode}
		n.Value = "false"
		if val {
			n.Value = "true"
		}
		return n, nil
	case int, int64, float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmtS("%v", val)}, nil
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: sanitizeUTF8(val), Tag: "!!str"}, nil
	case *orderedMap:
		node, err := val.MarshalYAML()
		if err != nil {
			return nil, err
		}
		if n, ok := node.(*yaml.Node); ok {
			return n, nil
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmtS("%v", node)}, nil
	case []any:
		var content []*yaml.Node
		for _, elem := range val {
			node, err := anyToYAMLNode(elem)
			if err != nil {
				return nil, err
			}
			content = append(content, node)
		}
		return &yaml.Node{Kind: yaml.SequenceNode, Content: content}, nil
	case map[string]any:
		// Fallback: sort keys for deterministic output
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var content []*yaml.Node
		for _, k := range keys {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
			valNode, err := anyToYAMLNode(val[k])
			if err != nil {
				return nil, err
			}
			content = append(content, keyNode, valNode)
		}
		return &yaml.Node{Kind: yaml.MappingNode, Content: content}, nil
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmtS("%v", val)}, nil
	}
}

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
	case *orderedMap:
		tbl := L.NewTable()
		for _, item := range val.items {
			L.SetField(tbl, item.Key, toLValue(L, item.Value))
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
	case *orderedMap:
		tbl := L.NewTable()
		for _, item := range val.items {
			L.SetField(tbl, item.Key, toLValue(L, item.Value))
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

// yamlFieldOrder defines the preferred field order for YAML output.
// Fields not in this list are sorted alphabetically after the listed fields.
// Note: Go map deduplicates keys; if a field appears in multiple contexts,
// use the same priority to ensure consistent ordering.
var yamlFieldOrder = map[string]int{
	// skeleton_index.yaml top-level
	"skeleton_version": 1, "project_name": 2, "generated_date": 3, "description": 4,
	"source_documents": 5, "node_tree": 6,
	// node_tree elements (description shared with top-level, priority 4)
	"node": 10, "status": 12, "sources": 13, "children": 14,
	// sources elements (also used in fragment)
	"document": 20, "document_path": 21, "chapter": 22, "fragment_id": 23,
	"level": 24, "start_line": 25, "end_line": 26,
	// fragment fields
	"title": 30, "heading_level": 31, "heading_path": 32,
	"preview": 36, "preview1": 37,
	"tables": 38, "images": 39,
	// source_documents elements
	"name": 50, "path": 51, "type": 52, "total_lines": 53,
	// table fields
	"headers": 60, "rows": 61, "row_count": 63, "col_count": 64,
	"merge_hints": 65, "nested_tables": 66, "has_error": 67, "error_message": 68,
}

// yamlFieldPriority returns a sort priority for a field name.
// Known fields get their defined priority; unknown fields get 1000 + alphabetical order.
func yamlFieldPriority(key string) int {
	if p, ok := yamlFieldOrder[key]; ok {
		return p
	}
	return 1000
}

// luaTableToOrderedMap converts a Lua table to *orderedMap,
// sorting keys by predefined field priority for consistent YAML output.
func luaTableToOrderedMap(tbl *lua.LTable) *orderedMap {
	// Collect all key-value pairs first
	type kv struct {
		key   string
		value lua.LValue
	}
	var entries []kv
	tbl.ForEach(func(k, v lua.LValue) {
		key := luaValueToString(k)
		entries = append(entries, kv{key: key, value: v})
	})

	// Sort by predefined field priority
	sort.SliceStable(entries, func(i, j int) bool {
		pi := yamlFieldPriority(entries[i].key)
		pj := yamlFieldPriority(entries[j].key)
		if pi != pj {
			return pi < pj
		}
		return entries[i].key < entries[j].key
	})

	// Build orderedMap with sorted entries
	result := &orderedMap{}
	for _, e := range entries {
		result.items = append(result.items, orderedMapItem{Key: e.key, Value: lValueToGoOrdered(e.value)})
	}
	return result
}

// lValueToGoOrdered converts an LValue to a Go value using ordered maps (*orderedMap).
// This is used by the YAML serialization path to preserve field order.
func lValueToGoOrdered(v lua.LValue) any {
	switch val := v.(type) {
	case lua.LBool:
		return bool(val)
	case lua.LNumber:
		return float64(val)
	case lua.LString:
		return string(val)
	case *lua.LTable:
		if isArrayTable(val) {
			return lTableToSliceOrdered(val)
		}
		return luaTableToOrderedMap(val)
	case *lua.LNilType:
		return nil
	default:
		return v.String()
	}
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

// lTableToSliceOrdered converts a Lua array table to []any using lValueToGoOrdered
// to preserve field order in nested maps.
func lTableToSliceOrdered(tbl *lua.LTable) []any {
	n := tbl.MaxN()
	if n == 0 {
		return nil
	}
	result := make([]any, n)
	for i := 1; i <= n; i++ {
		result[i-1] = lValueToGoOrdered(tbl.RawGetInt(i))
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

// sanitizeUTF8 replaces invalid UTF-8 byte sequences with the Unicode
// replacement character (U+FFFD). This prevents yaml.v3 from failing
// with "cannot marshal invalid UTF-8 data as !!str" when source files
// contain mixed or corrupted encoding.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString("\uFFFD")
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}
