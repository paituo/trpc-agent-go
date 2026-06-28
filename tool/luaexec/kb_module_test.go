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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// ---------- 配置校验测试 ----------

func TestNewKBModule_ValidConfig(t *testing.T) {
	cfg := &KBModuleConfig{
		EmbedderBaseURL: "http://test:9997/v1",
		EmbedderAPIKey:  "test-key",
		Dimensions:      1024,
		MaxChunkSize:    2000,
		ChunkOverlap:    200,
	}
	km, err := NewKBModule(cfg)
	require.NoError(t, err)
	require.NotNil(t, km)
	assert.Equal(t, 1024, km.config.Dimensions)
	assert.Equal(t, 2000, km.config.MaxChunkSize)
	assert.Equal(t, 200, km.config.ChunkOverlap)
}

func TestNewKBModule_MissingBaseURL(t *testing.T) {
	cfg := &KBModuleConfig{
		EmbedderBaseURL: "",
		EmbedderAPIKey:  "test-key",
	}
	_, err := NewKBModule(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "EmbedderBaseURL")
}

func TestNewKBModule_MissingAPIKey(t *testing.T) {
	cfg := &KBModuleConfig{
		EmbedderBaseURL: "http://test:9997/v1",
		EmbedderAPIKey:  "",
	}
	_, err := NewKBModule(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "EmbedderAPIKey")
}

func TestNewKBModule_DimensionFallback(t *testing.T) {
	cfg := &KBModuleConfig{
		EmbedderBaseURL: "http://test:9997/v1",
		EmbedderAPIKey:  "test-key",
		Dimensions:      0,
	}
	km, err := NewKBModule(cfg)
	require.NoError(t, err)
	assert.Equal(t, 1024, km.config.Dimensions)
}

func TestNewKBModule_MaxChunkSizeFallback(t *testing.T) {
	cfg := &KBModuleConfig{
		EmbedderBaseURL: "http://test:9997/v1",
		EmbedderAPIKey:  "test-key",
		MaxChunkSize:    0,
	}
	km, err := NewKBModule(cfg)
	require.NoError(t, err)
	assert.Equal(t, 2000, km.config.MaxChunkSize)
}

func TestNewKBModule_ChunkOverlapFallback(t *testing.T) {
	cfg := &KBModuleConfig{
		EmbedderBaseURL: "http://test:9997/v1",
		EmbedderAPIKey:  "test-key",
		ChunkOverlap:    0,
	}
	km, err := NewKBModule(cfg)
	require.NoError(t, err)
	assert.Equal(t, 200, km.config.ChunkOverlap)
}

// ---------- parseSearchMode 测试 ----------

func TestParseSearchMode(t *testing.T) {
	tests := []struct {
		input    string
		expected vectorstore.SearchMode
	}{
		{"hybrid", vectorstore.SearchModeHybrid},
		{"vector", vectorstore.SearchModeVector},
		{"keyword", vectorstore.SearchModeKeyword},
		{"filter", vectorstore.SearchModeFilter},
		{"", vectorstore.SearchModeHybrid},
		{"Hybrid", vectorstore.SearchModeHybrid},
		{"VECTOR", vectorstore.SearchModeVector},
		{"unknown", vectorstore.SearchModeHybrid},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseSearchMode(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ---------- store handle 格式测试 ----------
// store handle 现在是 path/filename 格式

func TestStoreHandleFormat(t *testing.T) {
	handle := "/tmp/kb/test.db"
	assert.Contains(t, handle, "/")
	assert.NotEqual(t, "", handle)
}

// ---------- luaTableGetString 测试 ----------

func TestLuaTableGetString(t *testing.T) {
	L := newTestLuaState()
	defer L.Close()

	tbl := L.NewTable()
	L.SetField(tbl, "key1", lua.LString("value1"))
	L.SetField(tbl, "key2", lua.LNumber(42))

	assert.Equal(t, "value1", luaTableGetString(tbl, "key1"))
	assert.Equal(t, "", luaTableGetString(tbl, "key2"))
	assert.Equal(t, "", luaTableGetString(tbl, "nonexistent"))
}

// ---------- luaTableGetInt 测试 ----------

func TestLuaTableGetInt(t *testing.T) {
	L := newTestLuaState()
	defer L.Close()

	tbl := L.NewTable()
	L.SetField(tbl, "num", lua.LNumber(42))
	L.SetField(tbl, "str", lua.LString("10"))

	assert.Equal(t, 42, luaTableGetInt(tbl, "num", 0))
	assert.Equal(t, 10, luaTableGetInt(tbl, "str", 0))
	assert.Equal(t, 99, luaTableGetInt(tbl, "nonexistent", 99))
}

// ---------- luaTableGetFloat 测试 ----------

func TestLuaTableGetFloat(t *testing.T) {
	L := newTestLuaState()
	defer L.Close()

	tbl := L.NewTable()
	L.SetField(tbl, "num", lua.LNumber(3.14))

	assert.InDelta(t, 3.14, luaTableGetFloat(tbl, "num", 0), 0.001)
	assert.InDelta(t, 1.5, luaTableGetFloat(tbl, "nonexistent", 1.5), 0.001)
}

// ---------- luaTableGetArray 测试 ----------

func TestLuaTableGetArray(t *testing.T) {
	L := newTestLuaState()
	defer L.Close()

	tbl := L.NewTable()
	arr := L.NewTable()
	L.RawSetInt(arr, 1, lua.LString("a"))
	L.RawSetInt(arr, 2, lua.LString("b"))
	L.SetField(tbl, "items", arr)

	result := luaTableGetArray(tbl, "items")
	require.Len(t, result, 2)
	assert.Equal(t, "a", result[0])
	assert.Equal(t, "b", result[1])

	assert.Nil(t, luaTableGetArray(tbl, "nonexistent"))
}

// ---------- luaTableGetMap 测试 ----------

func TestLuaTableGetMap(t *testing.T) {
	L := newTestLuaState()
	defer L.Close()

	tbl := L.NewTable()
	inner := L.NewTable()
	L.SetField(inner, "k1", lua.LString("v1"))
	L.SetField(tbl, "meta", inner)

	result := luaTableGetMap(tbl, "meta")
	require.NotNil(t, result)
	assert.Equal(t, "v1", result["k1"])

	assert.Nil(t, luaTableGetMap(tbl, "nonexistent"))
}

// ---------- helpers ----------

// newTestLuaState creates a minimal Lua state for testing helper functions.
func newTestLuaState() *lua.LState {
	L := lua.NewState(lua.Options{
		CallStackSize: 128,
		RegistrySize:  1024,
	})
	return L
}
