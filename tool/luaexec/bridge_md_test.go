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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMD_Parse(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local doc = md.parse("# Hello\n\nSome text"); return type(doc)`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "userdata", resp["result"])
}

func TestMD_ParseEmpty(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local doc = md.parse(""); return type(doc)`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "userdata", resp["result"])
}

func TestMD_ExtractTables(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	mdInput := "| 序号 | 型号 | 呼高 |\n|------|------|------|\n| 1 | ZBC1 | 24 |\n| 2 | ZBC2 | 30 |"
	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local doc = md.parse(%q)
local tables = md.extract_tables(doc)
local t = tables[1]
return {header_count = #t.headers, row_count = #t.rows, first_header = t.headers[1], first_cell = t.rows[1][1]}
`, mdInput),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, float64(3), r["header_count"])
	assert.Equal(t, float64(2), r["row_count"])
	assert.Equal(t, "序号", r["first_header"])
	assert.Equal(t, "1", r["first_cell"])
}

func TestMD_ExtractTablesNoTable(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = md.parse("# Just a heading\n\nSome paragraph text")
local tables = md.extract_tables(doc)
return #tables
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(0), resp["result"])
}

func TestMD_ParseTable(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	tableText := "| 型号 | 数量 |\n|------|------|\n| ZBC1 | 5 |\n| ZBC2 | 3 |"
	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local t = md.parse_table(%q)
return {headers = #t.headers, rows = #t.rows, first_header = t.headers[1], first_row_first_cell = t.rows[1][1]}
`, tableText),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, float64(2), r["headers"])
	assert.Equal(t, float64(2), r["rows"])
	assert.Equal(t, "型号", r["first_header"])
	assert.Equal(t, "ZBC1", r["first_row_first_cell"])
}

func TestMD_ParseTableNotTable(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local t, err = md.parse_table("not a table"); return err`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	errMap := resp["result"].(map[string]any)
	assert.Equal(t, ErrTypeBridge, errMap["type"])
}

func TestMD_DetectMerge(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	tableText := "| 型号 | 数量 |\n|------|------|\n| ZBC1 | 5 |\n| | 3 |"
	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local t = md.parse_table(%q)
local merge = md.detect_merge(t)
return {headers = #t.headers, rows = #t.rows, has_merge = merge.has_merge}
`, tableText),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, float64(2), r["headers"])
	assert.Equal(t, float64(2), r["rows"])
	assert.Equal(t, true, r["has_merge"])
}

func TestMD_DetectMergeNoMerge(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	tableText := "| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |"
	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local t = md.parse_table(%q)
local merge = md.detect_merge(t)
return merge.has_merge
`, tableText),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, false, resp["result"])
}

func TestMD_Alignments(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	tableText := "| Left | Center | Right |\n|:-----|:------:|------:|\n| a | b | c |"
	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local t = md.parse_table(%q)
return {a1 = t.alignments[1], a2 = t.alignments[2], a3 = t.alignments[3]}
`, tableText),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, "left", r["a1"])
	assert.Equal(t, "center", r["a2"])
	assert.Equal(t, "right", r["a3"])
}

func TestMD_ParseTableWithIndex(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Two tables in one document
	mdInput := "| A | B |\n|---|---|\n| 1 | 2 |\n\nSome text\n\n| C | D |\n|---|---|\n| 3 | 4 |"
	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local first = md.parse_table(%q, 1)
local second = md.parse_table(%q, 2)
return {first_header = first.headers[1], second_header = second.headers[1]}
`, mdInput, mdInput),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, "A", r["first_header"])
	assert.Equal(t, "C", r["second_header"])
}

func TestMD_ParseTableIndexOutOfRange(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	tableText := "| A | B |\n|---|---|\n| 1 | 2 |"
	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`local t, err = md.parse_table(%q, 999); return err`, tableText),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	errMap := resp["result"].(map[string]any)
	assert.Equal(t, ErrTypeBridge, errMap["type"])
}

func TestMD_ExtractTablesFromString(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Pass string directly instead of md.parse() result
	mdInput := "| X | Y |\n|---|---|\n| a | b |"
	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local tables = md.extract_tables(%q)
return {count = #tables, first_header = tables[1].headers[1]}
`, mdInput),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, float64(1), r["count"])
	assert.Equal(t, "X", r["first_header"])
}

func TestMD_DetectMergeFromString(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Pass string directly to detect_merge
	tableText := "| A | B |\n|---|---|\n| 1 | 2 |\n| | 3 |"
	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local merge = md.detect_merge(%q)
return merge.has_merge
`, tableText),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, true, resp["result"])
}
