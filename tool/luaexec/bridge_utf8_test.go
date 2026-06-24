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
)

func runLuaScript(t *testing.T, script string) map[string]any {
	t.Helper()
	ts, err := NewToolSet(WithDeniedModules("tool"))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	require.Len(t, tools, 1)
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{"script": script})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	return result.(map[string]any)
}

func TestUTF8_Len_Chinese(t *testing.T) {
	resp := runLuaScript(t, `return utf8.len("你好")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(2), resp["result"])
}

func TestUTF8_Len_Mixed(t *testing.T) {
	resp := runLuaScript(t, `return utf8.len("hello你好")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(7), resp["result"])
}

func TestUTF8_Len_Empty(t *testing.T) {
	resp := runLuaScript(t, `return utf8.len("")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(0), resp["result"])
}

func TestUTF8_Sub_Chinese(t *testing.T) {
	resp := runLuaScript(t, `return utf8.sub("你好世界", 2, 3)`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "好世", resp["result"])
}

func TestUTF8_Sub_NegativeIndex(t *testing.T) {
	resp := runLuaScript(t, `return utf8.sub("你好世界", -2)`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "世界", resp["result"])
}

func TestUTF8_Sub_OutOfRange(t *testing.T) {
	resp := runLuaScript(t, `return utf8.sub("你好", 3, 5)`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "", resp["result"])
}

func TestUTF8_Sub_DefaultJ(t *testing.T) {
	resp := runLuaScript(t, `return utf8.sub("你好世界", 2)`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "好世界", resp["result"])
}

func TestUTF8_Reverse_Chinese(t *testing.T) {
	resp := runLuaScript(t, `return utf8.reverse("你好")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "好你", resp["result"])
}

func TestUTF8_Upper_Lower(t *testing.T) {
	resp := runLuaScript(t, `return utf8.upper("hello") .. "-" .. utf8.lower("HELLO")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "HELLO-hello", resp["result"])
}

func TestUTF8_Char(t *testing.T) {
	resp := runLuaScript(t, `return utf8.char(0x4F60)`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "你", resp["result"])
}

func TestUTF8_Codepoint(t *testing.T) {
	resp := runLuaScript(t, `return utf8.codepoint("你")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(20320), resp["result"])
}

func TestUTF8_Codes(t *testing.T) {
	resp := runLuaScript(t, `
local result = {}
for pos, cp in utf8.codes("你好") do
    table.insert(result, pos .. ":" .. cp)
end
return table.concat(result, ",")
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "1:20320,4:22909", resp["result"])
}

func TestUTF8_Byteoffset(t *testing.T) {
	resp := runLuaScript(t, `return utf8.byteoffset("你好", 2)`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(4), resp["result"])
}

func TestUTF8_Validate_Valid(t *testing.T) {
	resp := runLuaScript(t, `return utf8.validate("你好")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, true, resp["result"])
}

func TestUTF8_Validate_Invalid(t *testing.T) {
	resp := runLuaScript(t, `return utf8.validate(string.char(0xFF, 0xFE))`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, false, resp["result"])
}

func TestUTF8_Validate_Empty(t *testing.T) {
	resp := runLuaScript(t, `return utf8.validate("")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, true, resp["result"])
}

func TestUTF8_Find_Chinese(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("你好123世界", "\\d+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "123", resp["result"])
}

func TestUTF8_Find_NoMatch(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("你好", "\\d+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Nil(t, resp["result"])
}

func TestUTF8_Find_CaptureGroups(t *testing.T) {
	resp := runLuaScript(t, `
local full, cap1, cap2 = utf8.find("hello 123", "(\\w+) (\\d+)")
return full .. ":" .. cap1 .. ":" .. cap2
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "hello 123:hello:123", resp["result"])
}

func TestUTF8_Find_WithInit(t *testing.T) {
	// "abc123def456": init=7 skips "abc123", starts from "def456"
	resp := runLuaScript(t, `return utf8.find("abc123def456", "\\d+", 7)`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "456", resp["result"])
}

func TestUTF8_Find_WithInit_Chinese(t *testing.T) {
	// "你好123世界456": init=6 skips "你好123", starts from "世界456"
	resp := runLuaScript(t, `return utf8.find("你好123世界456", "\\d+", 6)`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "456", resp["result"])
}

func TestUTF8_Find_WithNegativeInit(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("abc123def456", "\\d+", -4)`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "456", resp["result"])
}

func TestUTF8_Find_InitOutOfRange(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("hello", "\\d+", 3)`)
	assert.Equal(t, "success", resp["status"])
	assert.Nil(t, resp["result"])
}

func TestUTF8_Match(t *testing.T) {
	resp := runLuaScript(t, `return #utf8.match("hello 123", "\\d+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(1), resp["result"])
}

func TestUTF8_Gsub(t *testing.T) {
	resp := runLuaScript(t, `return utf8.gsub("hello 123", "\\d+", "NUM")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "hello NUM", resp["result"])
}

func TestUTF8_Gsub_CaptureRef(t *testing.T) {
	resp := runLuaScript(t, `return utf8.gsub("hello 123", "(\\w+)", "${1}_")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "hello_ 123_", resp["result"])
}

func TestUTF8_Matches(t *testing.T) {
	resp := runLuaScript(t, `return #utf8.matches("hello 123 world 456", "\\d+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(2), resp["result"])
}

func TestUTF8_Matches_CaptureGroups(t *testing.T) {
	resp := runLuaScript(t, `
local matches = utf8.matches("a1 b2", "(\\w)(\\d)")
local result = ""
for i, m in ipairs(matches) do
    result = result .. table.concat(m, ",") .. ";"
end
return result
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "a1,a,1;b2,b,2;", resp["result"])
}

func TestUTF8_Detect_UTF8(t *testing.T) {
	resp := runLuaScript(t, `return utf8.detect("你好")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "utf-8", resp["result"])
}

func TestUTF8_Detect_Empty(t *testing.T) {
	resp := runLuaScript(t, `return utf8.detect("")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "utf-8", resp["result"])
}

func TestUTF8_REAlias(t *testing.T) {
	resp := runLuaScript(t, `return re.find("hello", "\\w+") == utf8.find("hello", "\\w+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, true, resp["result"])
}

func TestUTF8_DeniedModule(t *testing.T) {
	ts, err := NewToolSet(WithDeniedModules("tool", "utf8"))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	require.Len(t, tools, 1)
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{"script": `return type(utf8)`})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "nil", resp["result"])
}

func TestUTF8_DeniedREAlias(t *testing.T) {
	ts, err := NewToolSet(WithDeniedModules("tool", "re"))
	require.NoError(t, err)
	defer ts.Close()

	tools := ts.Tools(context.Background())
	require.Len(t, tools, 1)
	ct := tools[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{"script": `return type(re) .. "," .. type(utf8)`})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "nil,table", resp["result"])
}

// ============================================================
// Lua-style regex compatibility tests
// ============================================================

// TestUTF8_Find_LuaPattern_Digit tests %d (Lua digit class) in utf8.find
func TestUTF8_Find_LuaPattern_Digit(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("你好123世界", "%d+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "123", resp["result"])
}

// TestUTF8_Find_LuaPattern_Whitespace tests %s (Lua whitespace class) in utf8.find
func TestUTF8_Find_LuaPattern_Whitespace(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("a b  c", "%s+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, " ", resp["result"])
}

// TestUTF8_Find_LuaPattern_Word tests %w (Lua word class) in utf8.find
func TestUTF8_Find_LuaPattern_Word(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("hello_123", "%w+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "hello_123", resp["result"])
}

// TestUTF8_Find_LuaPattern_Alpha tests %a (Lua alpha class) in utf8.find
func TestUTF8_Find_LuaPattern_Alpha(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("abc123", "%a+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "abc", resp["result"])
}

// TestUTF8_Find_LuaPattern_EscapedPipe tests %| (Lua escaped pipe) in utf8.find
func TestUTF8_Find_LuaPattern_EscapedPipe(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("a|b|c", "%|")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "|", resp["result"])
}

// TestUTF8_Find_LuaPattern_EscapedDot tests %. (Lua escaped dot) in utf8.find
func TestUTF8_Find_LuaPattern_EscapedDot(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("file.txt", "%.")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, ".", resp["result"])
}

// TestUTF8_Find_LuaPattern_EscapedParen tests %( and %) in utf8.find
func TestUTF8_Find_LuaPattern_EscapedParen(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("func(arg)", "%(%a+%)")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "(arg)", resp["result"])
}

// TestUTF8_Find_LuaPattern_LiteralPercent tests %% (literal %) in utf8.find
func TestUTF8_Find_LuaPattern_LiteralPercent(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("100%", "%%")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "%", resp["result"])
}

// TestUTF8_Find_LuaPattern_NonGreedy tests - (Lua non-greedy) in utf8.find
func TestUTF8_Find_LuaPattern_NonGreedy(t *testing.T) {
	// %d- matches zero or more digits (non-greedy), so it matches "" at position 0.
	// Use %d+%- to test non-greedy with a following literal: matches "1" (shortest digit run before "-")
	resp := runLuaScript(t, `return utf8.find("a123-456", "%d-%-")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "123-", resp["result"])
}

// TestUTF8_Find_LuaPattern_ChineseText tests Lua pattern matching Chinese text
func TestUTF8_Find_LuaPattern_ChineseText(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("工程概况和设计依据", "工程.-设计")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "工程概况和设计", resp["result"])
}

// TestUTF8_Find_LuaPattern_TableHeader tests matching Markdown table header with Lua pattern
func TestUTF8_Find_LuaPattern_TableHeader(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("| 字段 | 约束值 | 来源 |", "^%s*%|%s*字段%s*%|")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "| 字段 |", resp["result"])
}

// TestUTF8_Find_LuaPattern_Heading tests matching Markdown heading with Lua pattern
func TestUTF8_Find_LuaPattern_Heading(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("## 工程概况", "^#{1,6}%s+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "## ", resp["result"])
}

// TestUTF8_Find_LuaPattern_Chapter tests matching Chinese chapter title
func TestUTF8_Find_LuaPattern_Chapter(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("第3章 设计依据", "第%d+章")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "第3章", resp["result"])
}

// TestUTF8_Find_LuaPattern_TableSeparator tests matching Markdown table separator
func TestUTF8_Find_LuaPattern_TableSeparator(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("| --- | --- |", "^%s*%|[%s%-:|]+%|%s*$")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "| --- | --- |", resp["result"])
}

// TestUTF8_Find_LuaPattern_Image tests matching Markdown image syntax
func TestUTF8_Find_LuaPattern_Image(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("![图片](url.png)", "!%[.-%]%(.-%)")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "![图片](url.png)", resp["result"])
}

// TestUTF8_Find_LuaPattern_NoMatch tests Lua pattern with no match
func TestUTF8_Find_LuaPattern_NoMatch(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("你好世界", "%d+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Nil(t, resp["result"])
}

// TestUTF8_Find_LuaPattern_CaptureGroup tests Lua pattern with capture groups
func TestUTF8_Find_LuaPattern_CaptureGroup(t *testing.T) {
	resp := runLuaScript(t, `
local full, cap1, cap2 = utf8.find("hello 123 world", "(%a+)%s+(%d+)")
return full .. ":" .. cap1 .. ":" .. cap2
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "hello 123:hello:123", resp["result"])
}

// TestUTF8_Find_LuaPattern_WithInit tests Lua pattern with init parameter
func TestUTF8_Find_LuaPattern_WithInit(t *testing.T) {
	resp := runLuaScript(t, `return utf8.find("abc123def456", "%d+", 7)`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "456", resp["result"])
}

// TestUTF8_Match_LuaPattern tests utf8.match with Lua-style pattern
func TestUTF8_Match_LuaPattern(t *testing.T) {
	resp := runLuaScript(t, `return #utf8.match("hello 123 world 456", "%d+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(2), resp["result"])
}

// TestUTF8_Gsub_LuaPattern tests utf8.gsub with Lua-style pattern
func TestUTF8_Gsub_LuaPattern(t *testing.T) {
	resp := runLuaScript(t, `return utf8.gsub("hello 123", "%d+", "NUM")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "hello NUM", resp["result"])
}

// TestUTF8_Gsub_LuaBackref tests utf8.gsub with Lua-style back-reference %1
func TestUTF8_Gsub_LuaBackref(t *testing.T) {
	// (%a+) matches "hello" only (not "123"), so %1_ replaces "hello" with "hello_"
	resp := runLuaScript(t, `return utf8.gsub("hello 123", "(%a+)", "%1_")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "hello_ 123", resp["result"])
}

// TestUTF8_Gsub_LuaBackref_Chinese tests utf8.gsub with Lua back-ref on Chinese text
func TestUTF8_Gsub_LuaBackref_Chinese(t *testing.T) {
	resp := runLuaScript(t, `return utf8.gsub("工程概况", "(工程)", "【%1】")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "【工程】概况", resp["result"])
}

// TestUTF8_Gsub_LuaPattern_StripMarkdown tests stripping Markdown formatting with Lua patterns
func TestUTF8_Gsub_LuaPattern_StripMarkdown(t *testing.T) {
	resp := runLuaScript(t, `
local text = "**粗体文本**和*斜体*"
text = utf8.gsub(text, "%*%*(.-)%*%*", "%1")
text = utf8.gsub(text, "%*(.-)%*", "%1")
return text
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "粗体文本和斜体", resp["result"])
}

// TestUTF8_Gsub_LuaPattern_StripHtml tests stripping HTML tags with Lua patterns
func TestUTF8_Gsub_LuaPattern_StripHtml(t *testing.T) {
	resp := runLuaScript(t, `return utf8.gsub("<p>中文</p>", "<[^>]+>", "")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "中文", resp["result"])
}

// TestUTF8_Gsub_LuaPattern_ReplaceFullwidth tests replacing fullwidth chars with Lua patterns
func TestUTF8_Gsub_LuaPattern_ReplaceFullwidth(t *testing.T) {
	resp := runLuaScript(t, `return utf8.gsub("工程（新建）", "（", "("):gsub("）", ")")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "工程(新建)", resp["result"])
}

// TestUTF8_Matches_LuaPattern tests utf8.matches with Lua-style pattern
func TestUTF8_Matches_LuaPattern(t *testing.T) {
	resp := runLuaScript(t, `return #utf8.matches("hello 123 world 456", "%d+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(2), resp["result"])
}

// TestUTF8_Matches_LuaPattern_CaptureGroups tests utf8.matches with Lua pattern captures
func TestUTF8_Matches_LuaPattern_CaptureGroups(t *testing.T) {
	resp := runLuaScript(t, `
local matches = utf8.matches("a1 b2", "(%a)(%d)")
local result = ""
for i, m in ipairs(matches) do
    result = result .. table.concat(m, ",") .. ";"
end
return result
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "a1,a,1;b2,b,2;", resp["result"])
}

// TestUTF8_GoPattern_BackwardCompat verifies Go-style patterns still work
func TestUTF8_GoPattern_BackwardCompat(t *testing.T) {
	// Go-style patterns (with double backslash) must still work
	resp := runLuaScript(t, `return utf8.find("hello 123", "\\d+")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "123", resp["result"])
}

// TestUTF8_GoPattern_Gsub_BackwardCompat verifies Go-style gsub still works
func TestUTF8_GoPattern_Gsub_BackwardCompat(t *testing.T) {
	resp := runLuaScript(t, `return utf8.gsub("hello 123", "(\\w+)", "${1}_")`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "hello_ 123_", resp["result"])
}

// TestUTF8_LuaPattern_BalancedBracket verifies %b() returns nil (not supported)
func TestUTF8_LuaPattern_BalancedBracket(t *testing.T) {
	// %b() balanced match is not supported in Go regexp.
	// The luaPatternToGoRegex function converts %b to an unclosed [ which
	// causes regexp.Compile to fail, resulting in an error from utf8.find.
	resp := runLuaScript(t, `return utf8.find("(hello)", "%b()")`)
	// Note: Go regexp.Compile may accept "[" as literal in some versions,
	// so we accept either "error" or "success" with nil result.
	if resp["status"] == "error" {
		return // expected
	}
	assert.Equal(t, "success", resp["status"])
	assert.Nil(t, resp["result"])
}

// TestUTF8_LuaPattern_AllClasses tests all Lua character classes
func TestUTF8_LuaPattern_AllClasses(t *testing.T) {
	// Test %s (whitespace)
	r1 := runLuaScript(t, `return utf8.find("a b", "%s")`)
	assert.Equal(t, "success", r1["status"])
	assert.Equal(t, " ", r1["result"])

	// Test %S (non-whitespace)
	r2 := runLuaScript(t, `return utf8.find(" ", "%S")`)
	assert.Equal(t, "success", r2["status"])
	assert.Nil(t, r2["result"])

	// Test %D (non-digit)
	r3 := runLuaScript(t, `return utf8.find("123a", "%D")`)
	assert.Equal(t, "success", r3["status"])
	assert.Equal(t, "a", r3["result"])

	// Test %W (non-word)
	r4 := runLuaScript(t, `return utf8.find("abc ", "%W")`)
	assert.Equal(t, "success", r4["status"])
	assert.Equal(t, " ", r4["result"])

	// Test %p (punctuation)
	r5 := runLuaScript(t, `return utf8.find("hello, world", "%p")`)
	assert.Equal(t, "success", r5["status"])
	assert.Equal(t, ",", r5["result"])

	// Test %x (hex digit)
	r6 := runLuaScript(t, `return utf8.find("xyz", "%x")`)
	assert.Equal(t, "success", r6["status"])
	assert.Nil(t, r6["result"])

	r7 := runLuaScript(t, `return utf8.find("abc123", "%x")`)
	assert.Equal(t, "success", r7["status"])
	assert.Equal(t, "a", r7["result"])
}

// TestUTF8_LuaPattern_RealWorld_ExtractRawDrafts simulates the actual usage in extract_raw_drafts.lua
func TestUTF8_LuaPattern_RealWorld_ExtractRawDrafts(t *testing.T) {
	// Simulate the pattern used in extract_raw_drafts.lua for parsing design-skeleton
	// Note: string.match is used here (not utf8.find), so Lua regex syntax applies
	resp := runLuaScript(t, `
local line = "<!-- design-skeleton: EngineeringOverview -->"
local sp = string.match(line, "^%s*<!%-%-%s*design%-skeleton:%s*([^%|]+)%s*%-%-%>%s*$")
if sp then sp = string.match(sp, "^%s*(.-)%s*$") end
return sp
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "EngineeringOverview", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_SubcategoryStart simulates parsing "分类开始" markers
func TestUTF8_LuaPattern_RealWorld_SubcategoryStart(t *testing.T) {
	resp := runLuaScript(t, `
local line = "<!-- 分类开始：新建信息 -->"
local subcat_start = string.match(line, "分类开始：([^%-]+)")
if subcat_start then subcat_start = string.match(subcat_start, "^%s*(.-)%s*$") end
return subcat_start
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "新建信息", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_CostSkeleton simulates parsing cost-skeleton markers
func TestUTF8_LuaPattern_RealWorld_CostSkeleton(t *testing.T) {
	resp := runLuaScript(t, `
local line = "<!-- cost-skeleton: NewProject | output: 基本信息.新建信息 | key: | fields: 13 -->"
local sub_sp = string.match(line, "cost%-skeleton:%s*([^%|]+)")
return sub_sp
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "NewProject ", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_TableRow simulates parsing Markdown table rows
func TestUTF8_LuaPattern_RealWorld_TableRow(t *testing.T) {
	resp := runLuaScript(t, `
local line = "| 工程名称 | 必须存在 | 直提 | 说明文本 |"
local cells = {}
-- Skip leading empty cell from leading |, then parse remaining cells
local trimmed = string.match(line, "^%s*|%s*(.-)%s*|%s*$")
if trimmed then
    for cell in string.gmatch(trimmed, "([^|]*)") do
        local c = string.match(cell, "^%s*(.-)%s*$")
        if c ~= "" then
            table.insert(cells, c)
        end
    end
end
return #cells .. "|" .. cells[1] .. "|" .. cells[2]
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "4|工程名称|必须存在", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_HeadingDetection simulates heading detection in extract_outline.lua
func TestUTF8_LuaPattern_RealWorld_HeadingDetection(t *testing.T) {
	resp := runLuaScript(t, `
local line = "### 3.1 设计范围"
local r1 = utf8.find(line, "^#{1,6}%s")
local r2 = utf8.find("普通段落文本", "^#{1,6}%s")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil)
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_TableSeparator simulates table separator detection
func TestUTF8_LuaPattern_RealWorld_TableSeparator(t *testing.T) {
	resp := runLuaScript(t, `
local r1 = utf8.find("| --- | --- |", "^%s*%|[%s%-:|]+%|%s*$")
local r2 = utf8.find("| 列1 | 列2 |", "^%s*%|[%s%-:|]+%|%s*$")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil)
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_ImageExtraction simulates image extraction
func TestUTF8_LuaPattern_RealWorld_ImageExtraction(t *testing.T) {
	resp := runLuaScript(t, `
local line = "![图片1](url1.png) 文本 ![图片2](url2.png)"
local all_matches = utf8.matches(line, "!%[([^%]]*)%]%(([^%)]+)%)")
if not all_matches or #all_matches == 0 then return "no_match" end
local result = ""
for _, m in ipairs(all_matches) do
    if result ~= "" then result = result .. ";" end
    result = result .. (m[2] or "") .. "=" .. (m[3] or "")
end
return result
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "图片1=url1.png;图片2=url2.png", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_StripMarkdown simulates strip_markdown_formatting
func TestUTF8_LuaPattern_RealWorld_StripMarkdown(t *testing.T) {
	resp := runLuaScript(t, "\n"+
		`local text = "**粗体文本**和*斜体*和`+"`"+`代码`+"`"+`"`+"\n"+
		`text = utf8.gsub(text, "%*%*(.-)%*%*", "%1")`+"\n"+
		`text = utf8.gsub(text, "%*(.-)%*", "%1")`+"\n"+
		`text = utf8.gsub(text, "`+"`"+`(.-)`+"`"+`", "%1")`+"\n"+
		"return text\n")
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "粗体文本和斜体和代码", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_ChineseChapter simulates Chinese chapter parsing
func TestUTF8_LuaPattern_RealWorld_ChineseChapter(t *testing.T) {
	resp := runLuaScript(t, `
local _, chapter_text = utf8.find("  第3章 设计依据", "^%s*第%d+章[%s　]+(.+)$")
if not chapter_text then return "no_match" end
return chapter_text
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "设计依据", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_NumberedHeading simulates numbered heading parsing
func TestUTF8_LuaPattern_RealWorld_NumberedHeading(t *testing.T) {
	resp := runLuaScript(t, `
local _, num_prefix, num_text = utf8.find("  1.1.1 设计范围", "^(%s*%d+%.%d+%.%d+)[%s　]+(.+)$")
if not num_prefix then return "no_match" end
return num_prefix .. "|" .. num_text
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "  1.1.1|设计范围", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_TableTitle simulates table title detection
func TestUTF8_LuaPattern_RealWorld_TableTitle(t *testing.T) {
	resp := runLuaScript(t, `
local r1 = utf8.find("表 1-1 主要参数", "^表%s*%d")
local r2 = utf8.find("普通文本行", "^表%s*%d")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil)
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_TocLink simulates TOC link detection
func TestUTF8_LuaPattern_RealWorld_TocLink(t *testing.T) {
	resp := runLuaScript(t, `
local r1 = utf8.find("  - [1.1 设计依据](#design)", "^%s*%-%s*%[%d[%d.]*%s")
local r2 = utf8.find("- 普通列表项", "^%s*%-%s*%[%d[%d.]*%s")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil)
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_IsTocPageRef simulates TOC page reference detection
func TestUTF8_LuaPattern_RealWorld_IsTocPageRef(t *testing.T) {
	resp := runLuaScript(t, `
local r1 = utf8.find("......12", "%.%.%d")
local r2 = utf8.find("这不是目录", "%.%.%d")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil)
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_StripHtmlTags simulates HTML tag stripping
func TestUTF8_LuaPattern_RealWorld_StripHtmlTags(t *testing.T) {
	resp := runLuaScript(t, `
local text = "<p>带HTML标签的<b>中文</b>文本</p>"
text = utf8.gsub(text, "<[^>]+>", "")
return text
`)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "带HTML标签的中文文本", resp["result"])
}

// TestUTF8_LuaPattern_RealWorld_ChineseFullScenario simulates a complete Chinese processing scenario
func TestUTF8_LuaPattern_RealWorld_ChineseFullScenario(t *testing.T) {
	resp := runLuaScript(t, "\n"+
		`-- Simulate strip_markdown_formatting`+"\n"+
		`local title = "**第1章 总则（工程概况）**"`+"\n"+
		`title = utf8.gsub(title, "%*%*(.-)%*%*", "%1")`+"\n"+
		`title = utf8.gsub(title, "`+"`"+`(.-)`+"`"+`", "%1")`+"\n"+
		``+"\n"+
		`-- Simulate parse_heading`+"\n"+
		`local _, hashes, text = utf8.find("# 设计依据", "^(#{1,6})%s+(.+)$")`+"\n"+
		`local level = hashes and #hashes or 0`+"\n"+
		``+"\n"+
		`-- Simulate find_table_title`+"\n"+
		`local table_ref = utf8.find("表 1.1-1 主要技术参数", "^表%s*%d")`+"\n"+
		``+"\n"+
		`-- Simulate is_toc_title`+"\n"+
		`local toc_clean = utf8.gsub("**目  录**", "%*%*(.-)%*%*", "%1")`+"\n"+
		``+"\n"+
		"return title .. \"|\" .. text .. \"|\" .. level .. \"|\" .. tostring(table_ref ~= nil) .. \"|\" .. toc_clean\n")
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "第1章 总则（工程概况）|设计依据|1|true|目  录", resp["result"])
}
