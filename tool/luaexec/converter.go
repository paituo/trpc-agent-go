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
	case *orderedTable:
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
		// 将 orderedMap 转为 ordered_table userdata，保持字段顺序
		ot := &orderedTable{}
		for _, item := range val.items {
			ot.items = append(ot.items, orderedMapItem{Key: item.Key, Value: item.Value})
		}
		ud := L.NewUserData()
		ud.Metatable = L.GetTypeMetatable(orderedTableMetaKey)
		ud.Value = ot
		L.Push(ud)
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
		// 将 orderedMap 转为 ordered_table userdata，保持字段顺序
		ot := &orderedTable{}
		for _, item := range val.items {
			ot.items = append(ot.items, orderedMapItem{Key: item.Key, Value: item.Value})
		}
		ud := L.NewUserData()
		ud.Metatable = L.GetTypeMetatable(orderedTableMetaKey)
		ud.Value = ot
		return ud
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

// luaTableToOrderedMap converts a Lua table to *orderedMap,
// preserving field order for consistent YAML output.
//
// Ordering strategy:
//  1. If the table has a "_field_order" array field, use it to determine
//     the output order. Fields not in _field_order are appended at the end
//     in alphabetical order. The _field_order field itself is excluded from output.
//  2. Otherwise, sort keys alphabetically for deterministic output.
//
// _field_order is a Lua array of field names (strings) that defines the
// desired YAML key order. This allows Lua scripts to control output order
// without hardcoding business logic in Go code.
func luaTableToOrderedMap(tbl *lua.LTable) *orderedMap {
	// Collect all key-value pairs first
	type kv struct {
		key   string
		value lua.LValue
	}
	var entries []kv
	var fieldOrder []string

	tbl.ForEach(func(k, v lua.LValue) {
		key := luaValueToString(k)
		if key == "_field_order" {
			// Extract field order from array
			if tbl2, ok := v.(*lua.LTable); ok {
				tbl2.ForEach(func(ik, iv lua.LValue) {
					if s, ok2 := iv.(lua.LString); ok2 {
						fieldOrder = append(fieldOrder, string(s))
					}
				})
			}
			return // skip _field_order itself
		}
		entries = append(entries, kv{key: key, value: v})
	})

	if len(fieldOrder) > 0 {
		// Strategy 1: Use _field_order to determine output order
		orderMap := make(map[string]int, len(fieldOrder))
		for i, name := range fieldOrder {
			orderMap[name] = i
		}

		sort.SliceStable(entries, func(i, j int) bool {
			pi, hasI := orderMap[entries[i].key]
			pj, hasJ := orderMap[entries[j].key]
			switch {
			case hasI && hasJ:
				return pi < pj
			case hasI:
				return true // known fields before unknown
			case hasJ:
				return false
			default:
				return entries[i].key < entries[j].key
			}
		})
	} else {
		// Strategy 2: Sort alphabetically for deterministic output
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].key < entries[j].key
		})
	}

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
	case *lua.LUserData:
		// 如果是 ordered_table userdata，直接返回其内部 *orderedTable
		if ot, ok := val.Value.(*orderedTable); ok {
			return ot
		}
		return v.String()
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

// ============================================================
// orderedTable — Lua userdata 类型，保持字段插入顺序
// ============================================================

// orderedTableMetaKey 是 ordered_table userdata 的元表名称。
const orderedTableMetaKey = "ordered_table"

// orderedTable 是一个保持插入顺序的 LUA userdata。
// 内部使用 []orderedMapItem 按插入顺序存储键值对。
// 实现 yaml.Marshaler 接口，确保 YAML 序列化时按插入顺序输出。
type orderedTable struct {
	items []orderedMapItem
}

// MarshalYAML 实现 yaml.Marshaler 接口，按插入顺序输出 YAML。
func (ot *orderedTable) MarshalYAML() (any, error) {
	var content []*yaml.Node
	for _, item := range ot.items {
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

// registerOrderedTableType 注册 ordered_table 类型到 LUA VM。
// 注册后 Lua 脚本可以通过 ordered_table.new() 创建有序表，
// 通过 ordered_table.pairs(t) 遍历，通过 ordered_table.len(t) 获取长度。
func registerOrderedTableType(L *lua.LState) {
	mt := L.NewTypeMetatable(orderedTableMetaKey)

	// 构造函数
	L.SetField(mt, "new", L.NewFunction(orderedTableNew))

	// 元方法
	L.SetField(mt, "__index", L.NewFunction(orderedTableIndex))
	L.SetField(mt, "__newindex", L.NewFunction(orderedTableNewIndex))
	L.SetField(mt, "__len", L.NewFunction(orderedTableLen))
	L.SetField(mt, "__tostring", L.NewFunction(orderedTableToString))

	// 工具函数
	L.SetField(mt, "pairs", L.NewFunction(orderedTablePairs))
	L.SetField(mt, "len", L.NewFunction(orderedTableLen))
	L.SetField(mt, "unwrap", L.NewFunction(orderedTableUnwrap))
	L.SetField(mt, "wrap", L.NewFunction(orderedTableWrap))

	// 注册到全局
	L.SetGlobal("ordered_table", mt)
}

// orderedTableNew 创建新的有序表：ordered_table.new()
func orderedTableNew(L *lua.LState) int {
	ot := &orderedTable{}
	ud := L.NewUserData()
	ud.Metatable = L.GetTypeMetatable(orderedTableMetaKey)
	ud.Value = ot
	L.Push(ud)
	return 1
}

// orderedTableIndex 读取字段：t["key"] 或 t.key
func orderedTableIndex(L *lua.LState) int {
	ud := L.CheckUserData(1)
	key := L.CheckString(2)
	ot, ok := ud.Value.(*orderedTable)
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	for _, item := range ot.items {
		if item.Key == key {
			pushGoValue(L, item.Value)
			return 1
		}
	}
	L.Push(lua.LNil)
	return 1
}

// orderedTableNewIndex 设置字段：t["key"] = value 或 t.key = value
// 如果 key 已存在，更新值但不改变顺序；新 key 追加到末尾。
func orderedTableNewIndex(L *lua.LState) int {
	ud := L.CheckUserData(1)
	key := L.CheckString(2)
	val := lValueToGoOrdered(L.Get(3))
	ot, ok := ud.Value.(*orderedTable)
	if !ok {
		return 0
	}
	// 如果 key 已存在，更新值但不改变顺序
	for i, item := range ot.items {
		if item.Key == key {
			ot.items[i].Value = val
			return 0
		}
	}
	// 新 key，追加到末尾
	ot.items = append(ot.items, orderedMapItem{Key: key, Value: val})
	return 0
}

// orderedTableLen 获取字段数量：#t 或 ordered_table.len(t)
func orderedTableLen(L *lua.LState) int {
	ud := L.CheckUserData(1)
	ot, ok := ud.Value.(*orderedTable)
	if !ok {
		L.Push(lua.LNumber(0))
		return 1
	}
	L.Push(lua.LNumber(len(ot.items)))
	return 1
}

// orderedTableToString 转换为字符串：tostring(t)
func orderedTableToString(L *lua.LState) int {
	ud := L.CheckUserData(1)
	ot, ok := ud.Value.(*orderedTable)
	if !ok {
		L.Push(lua.LString("ordered_table: invalid"))
		return 1
	}
	L.Push(lua.LString("ordered_table: {" + fmtS("%d", len(ot.items)) + " fields}"))
	return 1
}

// orderedTablePairs 按插入顺序遍历：ordered_table.pairs(t)
// 返回迭代器函数、状态、初始索引，供 for k, v in ordered_table.pairs(t) do ... end 使用。
func orderedTablePairs(L *lua.LState) int {
	ud := L.CheckUserData(1)
	ot, ok := ud.Value.(*orderedTable)
	if !ok {
		L.Push(lua.LNil)
		return 1
	}

	idx := 0
	iter := L.NewFunction(func(L2 *lua.LState) int {
		if idx >= len(ot.items) {
			L2.Push(lua.LNil)
			return 1
		}
		item := ot.items[idx]
		idx++
		L2.Push(lua.LString(item.Key))
		pushGoValue(L2, item.Value)
		return 2
	})
	L.Push(iter)
	L.Push(lua.LNil)       // 无状态
	L.Push(lua.LNumber(0)) // 初始索引
	return 3
}

// orderedTableUnwrap 将 ordered_table 转换为普通 Lua table：ordered_table.unwrap(t)
// 转换后的 table 字段按字母序排列（与普通 table 行为一致）。
func orderedTableUnwrap(L *lua.LState) int {
	ud := L.CheckUserData(1)
	ot, ok := ud.Value.(*orderedTable)
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	tbl := L.NewTable()
	for _, item := range ot.items {
		L.SetField(tbl, item.Key, toLValue(L, item.Value))
	}
	L.Push(tbl)
	return 1
}

// orderedTableWrap 将普通 Lua table 包装为 ordered_table：ordered_table.wrap(t)
// 字段按字母序排列（因为普通 table 的遍历顺序不确定）。
func orderedTableWrap(L *lua.LState) int {
	tbl := L.CheckTable(1)
	ot := &orderedTable{}
	// 收集所有键值对
	tbl.ForEach(func(k, v lua.LValue) {
		key := luaValueToString(k)
		ot.items = append(ot.items, orderedMapItem{
			Key:   key,
			Value: lValueToGoOrdered(v),
		})
	})
	ud := L.NewUserData()
	ud.Metatable = L.GetTypeMetatable(orderedTableMetaKey)
	ud.Value = ot
	L.Push(ud)
	return 1
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
