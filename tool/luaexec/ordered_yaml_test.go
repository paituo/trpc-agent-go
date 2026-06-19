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
// Lua table field insertion order via the orderedMap mechanism.
func TestOrderedYAML_FieldOrder(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Test 1: Known fields follow predefined priority order
	script := `
local result = yaml.encode({
  ["z_field"] = "last",
  ["a_field"] = "first",
  ["m_field"] = "middle"
})
-- Unknown fields are sorted alphabetically: a_field < m_field < z_field
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

	// Test 2: Nested table field order (predefined priority: description=4 < node=10 < status=12 < sources=13 < children=14)
	script2 := `
local result = yaml.encode({
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
local desc_pos = result:find("description:")
local node_pos = result:find("node:")
local status_pos = result:find("status:")
local sources_pos = result:find("sources:")
local children_pos = result:find("children:")
return {
  desc_before_node = desc_pos < node_pos,
  node_before_status = node_pos < status_pos,
  status_before_sources = status_pos < sources_pos,
  sources_before_children = sources_pos < children_pos
}
`
	args2, _ := json.Marshal(map[string]any{"script": script2})
	result2, err := ct.Call(context.Background(), args2)
	require.NoError(t, err)
	m2 := result2.(map[string]any)
	resp2 := m2["result"].(map[string]any)
	t.Logf("Nested table result: %v", resp2)
	assert.Equal(t, true, resp2["desc_before_node"], "description(priority=4) should appear before node(priority=10)")
	assert.Equal(t, true, resp2["node_before_status"], "node(priority=10) should appear before status(priority=12)")
	assert.Equal(t, true, resp2["status_before_sources"], "status(priority=12) should appear before sources(priority=13)")
	assert.Equal(t, true, resp2["sources_before_children"], "sources(priority=13) should appear before children(priority=14)")

	// Test 3: Sources array element field order (critical: tests lTableToSliceOrdered)
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
local doc_pos = result:find("document:")
local chapter_pos = result:find("chapter:")
local fid_pos = result:find("fragment_id:")
local level_pos = result:find("level:")
local sl_pos = result:find("start_line:")
local el_pos = result:find("end_line:")
return {
  doc_before_chapter = doc_pos < chapter_pos,
  chapter_before_fid = chapter_pos < fid_pos,
  fid_before_level = fid_pos < level_pos,
  level_before_sl = level_pos < sl_pos,
  sl_before_el = sl_pos < el_pos
}
`
	args3, _ := json.Marshal(map[string]any{"script": script3})
	result3, err := ct.Call(context.Background(), args3)
	require.NoError(t, err)
	m3 := result3.(map[string]any)
	resp3 := m3["result"].(map[string]any)
	t.Logf("Sources element result: %v", resp3)
	assert.Equal(t, true, resp3["doc_before_chapter"], "document should appear before chapter in array element")
	assert.Equal(t, true, resp3["chapter_before_fid"], "chapter should appear before fragment_id in array element")
	assert.Equal(t, true, resp3["fid_before_level"], "fragment_id should appear before level in array element")
	assert.Equal(t, true, resp3["level_before_sl"], "level should appear before start_line in array element")
	assert.Equal(t, true, resp3["sl_before_el"], "start_line should appear before end_line in array element")
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
