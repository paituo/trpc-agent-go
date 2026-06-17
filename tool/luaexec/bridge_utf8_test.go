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