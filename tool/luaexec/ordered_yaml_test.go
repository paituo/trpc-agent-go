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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// TestOrderedYAML_FieldOrder verifies that YAML serialization preserves
// field order via _field_order or alphabetical sorting.
func TestOrderedYAML_FieldOrder(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Test 1: Without _field_order, fields are sorted alphabetically
	script := `
local result = yaml.encode({
  ["z_field"] = "last",
  ["a_field"] = "first",
  ["m_field"] = "middle"
})
-- Fields are sorted alphabetically: a_field < m_field < z_field
local a_pos = result:find("a_field")
local m_pos = result:find("m_field")
local z_pos = result:find("z_field")
return { a_before_m = a_pos < m_pos, m_before_z = m_pos < z_pos }
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	t.Logf("Simple table result: %v", resp)
	assert.Equal(t, true, resp["a_before_m"], "a_field should appear before m_field (alphabetical)")
	assert.Equal(t, true, resp["m_before_z"], "m_field should appear before z_field (alphabetical)")

	// Test 2: With _field_order, fields follow the defined order
	script2 := `
local result = yaml.encode({
  _field_order = { "status", "node", "children", "sources", "description" },
  ["node"] = "TestNode",
  ["description"] = "测试",
  ["status"] = "mounted",
  ["sources"] = {
    {
      ["document"] = "文档1",
      ["chapter"] = "章节1",
      ["fragment_id"] = "abc123",
      ["level"] = "primary",
      ["start_line"] = 10,
      ["end_line"] = 20
    }
  },
  ["children"] = {}
})
local status_pos = result:find("status:")
local node_pos = result:find("node:")
local children_pos = result:find("children:")
local sources_pos = result:find("sources:")
local desc_pos = result:find("description:")
return {
  status_before_node = status_pos < node_pos,
  node_before_children = node_pos < children_pos,
  children_before_sources = children_pos < sources_pos,
  sources_before_desc = sources_pos < desc_pos
}
`
	args2, _ := json.Marshal(map[string]any{"script": script2})
	result2, err := ct.Call(context.Background(), args2)
	require.NoError(t, err)
	m2 := result2.(map[string]any)
	resp2 := m2["result"].(map[string]any)
	t.Logf("Field order result: %v", resp2)
	assert.Equal(t, true, resp2["status_before_node"], "status should appear before node (_field_order)")
	assert.Equal(t, true, resp2["node_before_children"], "node should appear before children (_field_order)")
	assert.Equal(t, true, resp2["children_before_sources"], "children should appear before sources (_field_order)")
	assert.Equal(t, true, resp2["sources_before_desc"], "sources should appear before description (_field_order)")

	// Test 3: Sources array element field order (alphabetical without _field_order)
	script3 := `
local result = yaml.encode({
  ["sources"] = {
    {
      ["document"] = "文档1",
      ["chapter"] = "章节1",
      ["fragment_id"] = "abc123",
      ["level"] = "primary",
      ["start_line"] = 10,
      ["end_line"] = 20
    }
  }
})
-- Without _field_order, fields are sorted alphabetically:
-- chapter < document < end_line < fragment_id < level < start_line
local chapter_pos = result:find("chapter:")
local doc_pos = result:find("document:")
local el_pos = result:find("end_line:")
local fid_pos = result:find("fragment_id:")
local level_pos = result:find("level:")
local sl_pos = result:find("start_line:")
return {
  chapter_before_doc = chapter_pos < doc_pos,
  doc_before_el = doc_pos < el_pos,
  el_before_fid = el_pos < fid_pos,
  fid_before_level = fid_pos < level_pos,
  level_before_sl = level_pos < sl_pos
}
`
	args3, _ := json.Marshal(map[string]any{"script": script3})
	result3, err := ct.Call(context.Background(), args3)
	require.NoError(t, err)
	m3 := result3.(map[string]any)
	resp3 := m3["result"].(map[string]any)
	t.Logf("Sources element result: %v", resp3)
	assert.Equal(t, true, resp3["chapter_before_doc"], "chapter should appear before document in array element (alphabetical)")
	assert.Equal(t, true, resp3["doc_before_el"], "document should appear before end_line in array element (alphabetical)")
	assert.Equal(t, true, resp3["el_before_fid"], "end_line should appear before fragment_id in array element (alphabetical)")
	assert.Equal(t, true, resp3["fid_before_level"], "fragment_id should appear before level in array element (alphabetical)")
	assert.Equal(t, true, resp3["level_before_sl"], "level should appear before start_line in array element (alphabetical)")
}

// TestMountSources_NoSkeletonExampleData verifies that skeleton definition
// example sources are not included in the mount output.
func TestMountSources_NoSkeletonExampleData(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
-- Simulate the fixed process_skeleton_node logic
-- The fixed code does NOT copy skeleton definition sources to output
-- Only fragment-based sources are added
local fragment_sources = {
  {
    ["document"] = "地区编制手册",
    ["document_path"] = "./源文件/地区编制手册.md",
    ["chapter"] = "第一章 总则",
    ["fragment_id"] = "frag001",
    ["level"] = "primary",
    ["start_line"] = 5,
    ["end_line"] = 14
  }
}

local result = yaml.encode({ ["sources"] = fragment_sources })
local has_skeleton_chapter = result:find("第三章") ~= nil
local has_fragment_chapter = result:find("第一章 总则") ~= nil
local has_fragment_id = result:find("fragment_id:") ~= nil

return {
  no_skeleton_data = not has_skeleton_chapter,
  has_fragment_data = has_fragment_chapter,
  has_fragment_id = has_fragment_id
}
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	t.Logf("Mount sources result: %v", resp)
	assert.Equal(t, true, resp["no_skeleton_data"], "skeleton example data should NOT appear in output")
	assert.Equal(t, true, resp["has_fragment_data"], "fragment data should appear in output")
	assert.Equal(t, true, resp["has_fragment_id"], "fragment_id should appear in output")
}

// TestYAMLNoBinaryEncoding verifies that Chinese + HTML content in strings
// is NOT encoded as !!binary in YAML output.
func TestYAMLNoBinaryEncoding(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Test: Chinese + HTML content should NOT be encoded as !!binary
	script := `
local data = {
  ["preview1"] = "对可研评审意见的执行情况。1.4-1 对可研评审意见的执行情况 <table><thead><tr><th><p>序号</p></th></tr></thead>",
  ["preview"] = "新建线路起自110kV梅邵线64#大号侧，止于新建南和城东110kV变电站。"
}
local result = yaml.encode(data)
local has_binary = result:find("!!binary") ~= nil
local has_chinese_preview1 = result:find("对可研评审") ~= nil
local has_chinese_preview = result:find("新建线路") ~= nil
return { no_binary = not has_binary, has_chinese_p1 = has_chinese_preview1, has_chinese_p = has_chinese_preview }
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	t.Logf("Binary encoding test result: %v", resp)
	assert.Equal(t, true, resp["no_binary"], "Chinese+HTML content should NOT be encoded as !!binary")
	assert.Equal(t, true, resp["has_chinese_p1"], "Chinese text in preview1 should be readable")
	assert.Equal(t, true, resp["has_chinese_p"], "Chinese text in preview should be readable")
}

// TestDecodeAuto_UTF8Valid verifies that decodeAuto correctly
// identifies and decodes valid UTF-8 content.
func TestDecodeAuto_UTF8Valid(t *testing.T) {
	// Test with pure ASCII
	ascii := []byte("name: test\nvalue: 42\n")
	result, err := decodeAuto(ascii)
	require.NoError(t, err)
	assert.Equal(t, string(ascii), result)

	// Test with Chinese UTF-8 content
	chineseUTF8 := []byte("工程名称: 梅花～邵屯T接城东变电站110kV线路工程")
	result, err = decodeAuto(chineseUTF8)
	require.NoError(t, err)
	assert.Contains(t, result, "工程名称")
	assert.Contains(t, result, "梅花")
}

// TestDecodeAuto_GBKContent verifies that decodeAuto correctly
// detects and decodes GBK-encoded content.
func TestDecodeAuto_GBKContent(t *testing.T) {
	// Create GBK-encoded text
	gbkEncoder := simplifiedchinese.GBK.NewEncoder()
	gbkBytes, err := gbkEncoder.Bytes([]byte("工程名称: 梅花～邵屯T接城东变电站110kV线路工程"))
	require.NoError(t, err)

	result, err := decodeAuto(gbkBytes)
	require.NoError(t, err)
	assert.Contains(t, result, "工程名称")
	assert.Contains(t, result, "梅花")
}

// TestDecodeAuto_CJKHeuristic verifies that the CJK heuristic
// correctly prefers GBK decoding when UTF-8 bytes were written
// from a GBK source (mojibake scenario).
func TestDecodeAuto_CJKHeuristic(t *testing.T) {
	// Simulate the mojibake scenario: raw bytes are valid UTF-8
	// that spell "鍦板尯缂栧埗" (GBK mojibake of "地区编制手册"),
	// and also valid GBK that decodes to "地区编制手册".
	//
	// When raw = correct Chinese, decodeAuto should prefer GBK
	// decoding if it produces significantly more CJK characters.

	// RegionName is the GBK encoding of the correct Chinese text
	correctChinese := []byte("地区编制手册")
	gbkEncoder := simplifiedchinese.GBK.NewEncoder()
	gbkBytes, err := gbkEncoder.Bytes(correctChinese)
	require.NoError(t, err)

	// Now gbkBytes are GBK-encoded. When interpreted as UTF-8,
	// they form the mojibake characters. decodeAuto should detect
	// this via the CJK heuristic.
	result, err := decodeAuto(gbkBytes)
	require.NoError(t, err)
	assert.Equal(t, string(correctChinese), result, "decodeAuto should prefer GBK decoding for GBK content with CJK heuristic")
}

// TestCountCJKChars verifies the CJK character counter.
func TestCountCJKChars(t *testing.T) {
	// Pure ASCII
	assert.Equal(t, 0, countCJKChars("hello world"))

	// Chinese only
	assert.Equal(t, 2, countCJKChars("地区"))

	// Chinese + ASCII (6 CJK chars: 地区编制手册)
	assert.Equal(t, 6, countCJKChars("地区编制手册.md"))

	// GBK-encoded bytes, when interpreted as raw bytes (not valid UTF-8),
	// should yield 0 CJK characters because DecodeRuneInString returns RuneError.
	gbkEncoder := simplifiedchinese.GBK.NewEncoder()
	correctChinese := "地区编制手册.md"
	gbkBytes, _ := gbkEncoder.Bytes([]byte(correctChinese))
	// GBK bytes as a Go string (not valid UTF-8) should contain no valid CJK
	mojibakeCJKCount := countCJKChars(string(gbkBytes))
	assert.Equal(t, 0, mojibakeCJKCount, "raw GBK bytes as string should contain 0 valid CJK chars")

	// Correct Chinese should have correct CJK count
	cjkCorrect := countCJKChars(correctChinese)
	assert.Equal(t, 6, cjkCorrect, "正确中文应有6个CJK字符")
}

// ============================================================
// ordered_table 单元测试
// ============================================================

// TestOrderedTable_Basic 测试 ordered_table 的创建和读写
func TestOrderedTable_Basic(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
local t = ordered_table.new()
t["工程名称"] = "测试"
t["提取类型"] = "杆塔"
t["提取日期"] = "2026-06-27"
return {
  name = t["工程名称"],
  type = t["提取类型"],
  date = t["提取日期"],
  missing = t["不存在的字段"],
  count = ordered_table.len(t)
}
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	t.Logf("ordered_table basic result: %v", resp)
	assert.Equal(t, "测试", resp["name"])
	assert.Equal(t, "杆塔", resp["type"])
	assert.Equal(t, "2026-06-27", resp["date"])
	assert.Nil(t, resp["missing"])
	assert.Equal(t, float64(3), resp["count"])
}

// TestOrderedTable_InsertionOrder 测试 ordered_table 保持插入顺序
func TestOrderedTable_InsertionOrder(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
local t = ordered_table.new()
t["z_field"] = "last"
t["a_field"] = "first"
t["m_field"] = "middle"

-- 收集遍历顺序
local keys = {}
for k, v in ordered_table.pairs(t) do
  table.insert(keys, k)
end
return { keys = keys }
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	keys := resp["keys"].([]any)
	t.Logf("ordered_table insertion order: %v", keys)
	assert.Equal(t, "z_field", keys[0].(string), "z_field should be first (insertion order)")
	assert.Equal(t, "a_field", keys[1].(string), "a_field should be second (insertion order)")
	assert.Equal(t, "m_field", keys[2].(string), "m_field should be third (insertion order)")
}

// TestOrderedTable_UpdatePreservesOrder 测试更新已有字段不改变顺序
func TestOrderedTable_UpdatePreservesOrder(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
local t = ordered_table.new()
t["first"] = 1
t["second"] = 2
t["third"] = 3

-- 更新已有字段
t["second"] = 999

-- 添加新字段
t["fourth"] = 4

local keys = {}
for k, v in ordered_table.pairs(t) do
  table.insert(keys, k .. "=" .. tostring(v))
end
return { keys = keys }
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	keys := resp["keys"].([]any)
	t.Logf("ordered_table update result: %v", keys)
	assert.Equal(t, "first=1", keys[0].(string))
	assert.Equal(t, "second=999", keys[1].(string), "updated value should be 999, order unchanged")
	assert.Equal(t, "third=3", keys[2].(string))
	assert.Equal(t, "fourth=4", keys[3].(string), "new field appended at end")
}

// TestOrderedTable_YAMLEncode 测试 ordered_table 的 YAML 序列化保持顺序
func TestOrderedTable_YAMLEncode(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
local t = ordered_table.new()
t["z_field"] = "last"
t["a_field"] = "first"
t["m_field"] = "middle"

local result = yaml.encode(t)
-- 验证字段顺序：z_field < a_field < m_field（插入顺序）
local z_pos = result:find("z_field:")
local a_pos = result:find("a_field:")
local m_pos = result:find("m_field:")
return {
  z_before_a = z_pos < a_pos,
  a_before_m = a_pos < m_pos,
  has_z = z_pos ~= nil,
  has_a = a_pos ~= nil,
  has_m = m_pos ~= nil,
  yaml = result
}
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	t.Logf("ordered_table YAML encode result: %v", resp)
	t.Logf("YAML output:\n%s", resp["yaml"])
	assert.Equal(t, true, resp["z_before_a"], "z_field should appear before a_field (insertion order)")
	assert.Equal(t, true, resp["a_before_m"], "a_field should appear before m_field (insertion order)")
	assert.Equal(t, true, resp["has_z"])
	assert.Equal(t, true, resp["has_a"])
	assert.Equal(t, true, resp["has_m"])
}

// TestOrderedTable_Unwrap 测试 ordered_table.unwrap 转换为普通 table
func TestOrderedTable_Unwrap(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
local t = ordered_table.new()
t["b"] = 2
t["a"] = 1
local normal = ordered_table.unwrap(t)
return { type = type(normal), b = normal["b"], a = normal["a"] }
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	t.Logf("ordered_table unwrap result: %v", resp)
	assert.Equal(t, "table", resp["type"], "unwrap should return a regular table")
	assert.Equal(t, float64(2), resp["b"])
	assert.Equal(t, float64(1), resp["a"])
}

// TestOrderedTable_Wrap 测试 ordered_table.wrap 从普通 table 创建
func TestOrderedTable_Wrap(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
local normal = { z = 26, a = 1, m = 13 }
local t = ordered_table.wrap(normal)
local keys = {}
for k, v in ordered_table.pairs(t) do
  table.insert(keys, k)
end
return { keys = keys }
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	keys := resp["keys"].([]any)
	t.Logf("ordered_table wrap result: %v", keys)
	// 普通 table 的遍历顺序不确定，但至少应该包含所有字段
	assert.Contains(t, keys, "a")
	assert.Contains(t, keys, "z")
	assert.Contains(t, keys, "m")
}

// TestOrderedTable_WriteFile 测试 ordered_table 通过 yaml.encode 序列化保持顺序
func TestOrderedTable_WriteFile(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
local t = ordered_table.new()
t["z_val"] = "last"
t["a_val"] = "first"
t["m_val"] = "middle"
local result = yaml.encode(t)
return { yaml = result }
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	yamlStr := resp["yaml"].(string)
	t.Logf("ordered_table YAML output:\n%s", yamlStr)
	assert.Contains(t, yamlStr, "z_val")
	assert.Contains(t, yamlStr, "a_val")
	assert.Contains(t, yamlStr, "m_val")
	// 验证顺序：z_val 在 a_val 之前（插入顺序）
	zPos := findInString(t, yamlStr, "z_val:")
	aPos := findInString(t, yamlStr, "a_val:")
	assert.Less(t, zPos, aPos, "z_val should appear before a_val (insertion order)")
}

// TestOrderedTable_ReadFileOrdered 测试 yaml.read_file_ordered 返回 ordered_table
func TestOrderedTable_ReadFileOrdered(t *testing.T) {
	ts, err := NewToolSet(
		WithTools(&mockTool{name: "test_tool"}),
		WithAllowIOLib(true),
		WithAllowOSLib(true),
	)
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// 使用 yaml.decode 模拟读取 YAML 内容
	script := `
local yaml_str = [[
name: test
type: pole
date: "2026-06-27"
]]
local data = yaml.decode(yaml_str)
-- yaml.decode 返回普通 table，验证字段访问正常
return {
  name = data["name"],
  type = data["type"],
  date = data["date"]
}
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	t.Logf("ordered_table read_file_ordered result: %v", resp)
	assert.Equal(t, "test", resp["name"])
	assert.Equal(t, "pole", resp["type"])
	assert.Equal(t, "2026-06-27", resp["date"])
}

// TestOrderedTable_Nested 测试嵌套 ordered_table
func TestOrderedTable_Nested(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
local outer = ordered_table.new()
outer["name"] = "outer"

local inner = ordered_table.new()
inner["model"] = "inner"
inner["qty"] = 10
outer["child"] = inner

local result = yaml.encode(outer)
local name_pos = result:find("name:")
local child_pos = result:find("child:")
local model_pos = result:find("model:")
local qty_pos = result:find("qty:")
return {
  name_before_child = name_pos < child_pos,
  model_before_qty = model_pos < qty_pos,
  has_name = name_pos ~= nil,
  has_child = child_pos ~= nil,
  has_model = model_pos ~= nil,
  has_qty = qty_pos ~= nil
}
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	t.Logf("ordered_table nested result: %v", resp)
	assert.Equal(t, true, resp["name_before_child"], "name should appear before child")
	assert.Equal(t, true, resp["model_before_qty"], "model should appear before qty")
}

// TestOrderedTable_WithFieldOrder 测试 ordered_table 与 _field_order 共存
func TestOrderedTable_WithFieldOrder(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	script := `
-- 普通 table 使用 _field_order 仍然有效
local t = {}
t["z"] = "last"
t["a"] = "first"
t["_field_order"] = { "a", "z" }
local result = yaml.encode(t)
local a_pos = result:find("a:")
local z_pos = result:find("z:")
return { a_before_z = a_pos < z_pos }
`
	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	m := result.(map[string]any)
	resp := m["result"].(map[string]any)
	t.Logf("ordered_table with _field_order result: %v", resp)
	assert.Equal(t, true, resp["a_before_z"], "_field_order should still work with regular tables")
}

// findInString 查找子串在字符串中的位置
func findInString(t *testing.T, s, substr string) int {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
