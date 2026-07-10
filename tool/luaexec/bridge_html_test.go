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

func TestHTML_Parse(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local doc = html.parse("<table><tr><td>hello</td></tr></table>"); return type(doc)`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "userdata", resp["result"])
}

func TestHTML_ParseEmpty(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `local doc = html.parse(""); return type(doc)`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "userdata", resp["result"])
}

func TestHTML_FindAndFindAll(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	htmlInput := `<table><tr><td rowspan="2">A</td><td>B</td></tr><tr><td>C</td></tr></table>`
	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local doc = html.parse(%q)
local tds = html.find_all(doc, "td")
local count = #tds
local first_text = html.get_text(tds[1])
local first_rowspan = html.get_attr(tds[1], "rowspan")
return {count = count, first_text = first_text, first_rowspan = first_rowspan}
`, htmlInput),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, float64(3), r["count"])
	assert.Equal(t, "A", r["first_text"])
	assert.Equal(t, "2", r["first_rowspan"])
}

func TestHTML_GetTextAndAttr(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<div class='main'><p>hello world</p></div>")
local p = html.find(doc, "p")
local text = html.get_text(p)
local div = html.find(doc, "div")
local cls = html.get_attr(div, "class")
local name = html.tag_name(p)
local kids = html.children(div)
return {text = text, class = cls, tag = name, child_count = #kids}
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, "hello world", r["text"])
	assert.Equal(t, "main", r["class"])
	assert.Equal(t, "p", r["tag"])
	assert.Equal(t, float64(1), r["child_count"])
}

func TestHTML_GetAttrNonexistent(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<div id='test'>content</div>")
local div = html.find(doc, "div")
local missing = html.get_attr(div, "nonexistent")
return missing
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Nil(t, resp["result"])
}

func TestHTML_FaultTolerance(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Missing closing tags, unquoted attributes
	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<table><tr><td colspan=2>data<tr><td>more</table>")
local tds = html.find_all(doc, "td")
return #tds
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(2), resp["result"])
}

func TestHTML_SelectCSS(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<div id='main'><p class='intro'>Hello</p><p class='body'>World</p></div>")
local p = html.select(doc, "p.intro")
local text = html.get_text(p)
local div = html.select(doc, "#main")
local div_text = html.get_text(div)
return {intro = text, main = div_text}
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, "Hello", r["intro"])
	assert.Contains(t, r["main"], "Hello")
	assert.Contains(t, r["main"], "World")
}

func TestHTML_SelectAllCSS(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<div class='item'>A</div><div class='item'>B</div><div class='other'>C</div>")
local items = html.select_all(doc, ".item")
return #items
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(2), resp["result"])
}

func TestHTML_MethodStyleChaining(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<div><p>inner</p></div>")
local div = doc:find("div")
local p = div:find("p")
local text = p:get_text()
return text
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "inner", resp["result"])
}

func TestHTML_GetTextWithSeparator(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<div><p>Hello</p><p>World</p></div>")
local div = html.find(doc, "div")
local text_default = html.get_text(div)
local text_sep = html.get_text(div, ", ")
return {default = text_default, sep = text_sep}
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, "Hello World", r["default"])
	assert.Equal(t, "Hello, World", r["sep"])
}

func TestHTML_AllChildren(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<div>text node<p>element</p></div>")
local div = html.find(doc, "div")
local all = html.all_children(div)
local only_elem = html.children(div)
return {all_count = #all, elem_count = #only_elem}
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	r := resp["result"].(map[string]any)
	assert.Equal(t, float64(2), r["all_count"])   // text node + <p>
	assert.Equal(t, float64(1), r["elem_count"])   // only <p>
}

func TestHTML_Parent(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<div><p>child</p></div>")
local p = html.find(doc, "p")
local parent = html.parent(p)
local name = html.tag_name(parent)
return name
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "div", resp["result"])
}

func TestHTML_NestedTable(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	// Nested tables
	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<table><tr><td><table><tr><td>inner</td></tr></table></td></tr></table>")
local tables = html.find_all(doc, "table")
return #tables
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(2), resp["result"])
}

func TestHTML_StripTagsViaGetText(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	t.Run("table_html_extract_text", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"script": `
local doc = html.parse("<table><tr><td>名称</td><td>值</td></tr></table>")
local text = html.get_text(doc)
-- html.get_text returns text with space separator by default
return text
`,
		})
		result, err := ct.Call(context.Background(), args)
		require.NoError(t, err)
		resp := result.(map[string]any)
		assert.Equal(t, "success", resp["status"])
		assert.Contains(t, resp["result"].(string), "名称")
		assert.Contains(t, resp["result"].(string), "值")
	})

	t.Run("complex_html_strip_tags", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"script": `
local html_str = "<div><p>hello <b>world</b></p><ul><li>item1</li><li>item2</li></ul></div>"
local doc = html.parse(html_str)
local text = html.get_text(doc)
-- Should contain all text content without HTML tags
return text
`,
		})
		result, err := ct.Call(context.Background(), args)
		require.NoError(t, err)
		resp := result.(map[string]any)
		assert.Equal(t, "success", resp["status"])
		text := resp["result"].(string)
		assert.Contains(t, text, "hello")
		assert.Contains(t, text, "world")
		assert.Contains(t, text, "item1")
		assert.Contains(t, text, "item2")
	})

	t.Run("fallback_regex_strip", func(t *testing.T) {
		// Use utf8.gsub to strip tags (fallback path when html.parse fails)
		args, _ := json.Marshal(map[string]any{
			"script": `
local html_str = "<p>hello <b>world</b></p>"
local text = utf8.gsub(html_str, "<[^>]+>", "")
return text
`,
		})
		result, err := ct.Call(context.Background(), args)
		require.NoError(t, err)
		resp := result.(map[string]any)
		assert.Equal(t, "success", resp["status"])
		assert.Equal(t, "hello world", resp["result"])
	})

	t.Run("empty_html_string", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"script": `
local doc = html.parse("")
local text = html.get_text(doc)
return text
`,
		})
		result, err := ct.Call(context.Background(), args)
		require.NoError(t, err)
		resp := result.(map[string]any)
		assert.Equal(t, "success", resp["status"])
	})

	t.Run("html_with_attributes_strip", func(t *testing.T) {
		// Simulate the exact pattern used in html_to_plain_text:
		// table.insert(processed, strip_html_tags(before))
		args, _ := json.Marshal(map[string]any{
			"script": `
local before = "<span class='x'>some text</span> before table"
local ok, doc = pcall(html.parse, before)
if ok and doc then
  local ok2, text = pcall(html.get_text, doc)
  if ok2 and text then
    return text
  end
end
return nil
`,
		})
		result, err := ct.Call(context.Background(), args)
		require.NoError(t, err)
		resp := result.(map[string]any)
		assert.Equal(t, "success", resp["status"])
		if resp["result"] != nil {
			text := resp["result"].(string)
			assert.Contains(t, text, "some text")
			assert.Contains(t, text, "before table")
		}
	})
}

// ==================== htmltable.parse_html 多行表头测试 ====================

func TestHTMLTable_ParseMultiRowHeader(t *testing.T) {
	// 杆塔明细表 HTML — 首行有 rowspan=2 和 colspan=2
	// 第二行是子表头：数量、单位、总量、单重(kg)
	htmlInput := `<table><tr><td rowspan="2">序号</td><td rowspan="2">名称</td><td rowspan="2">新铁塔型号</td><td rowspan="2">呼高</td><td rowspan="2">塔全高(m)</td><td rowspan="2">单基重(kg)</td><td colspan="2">数量统计</td><td>重量统计(kg)</td><td></td></tr><tr><td>数量</td><td>单位</td><td>总量</td><td>单重(kg)</td></tr><tr><td>1</td><td>钢杆</td><td>110-DD21GS-J4</td><td>18</td><td>25.60</td><td>17550.00</td><td>1</td><td>基</td><td>17550.00</td><td></td></tr></table>`
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local result = htmltable.parse_html(%q)
-- 验证表头
local h = result.headers
-- h[1] = "序号", h[2] = "名称", h[3] = "新铁塔型号", h[4] = "呼高", h[5] = "塔全高(m)", h[6] = "单基重(kg)"
-- h[7] = "数量统计>数量", h[8] = "数量统计>单位", h[9] = "重量统计(kg)>总量", h[10] = ">单重(kg)" (or "单重(kg)")
-- h[10] might be ">单重(kg)" because row 0 has empty cell, row 1 has "单重(kg)"

-- 验证数据行
local rows = result.rows
-- row_count 为总占用行数（表头+数据），data_row_count 为纯数据行数
return {headers = h, row_count = result.row_count, data_row_count = result.data_row_count, first_row = rows[1], col_count = result.col_count}
`, htmlInput),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	require.Equal(t, "success", resp["status"])

	r := resp["result"].(map[string]any)
	headers, _ := r["headers"].([]any)
	t.Logf("  headers = %v", headers)
	rowCount, _ := r["row_count"].(float64)
	t.Logf("  row_count (total) = %v", rowCount)
	dataRowCount, _ := r["data_row_count"].(float64)
	t.Logf("  data_row_count = %v", dataRowCount)
	firstRow, _ := r["first_row"].([]any)
	t.Logf("  first_row = %v", firstRow)
	colCount, _ := r["col_count"].(float64)
	t.Logf("  col_count = %v", colCount)

	// 验证列数
	assert.Equal(t, float64(10), colCount, "应有10列")

	// 验证表头包含子表头合并
	if len(headers) >= 7 {
		assert.Contains(t, headers[6].(string), ">", "列7应包含 > 符号（子表头合并）")
		t.Logf("  列7表头: %s", headers[6])
	}
	if len(headers) >= 10 {
		t.Logf("  列10表头: %s", headers[9])
	}

	// 验证总占用行数：应为3行（2行表头 + 1行数据）
	assert.Equal(t, float64(3), rowCount, "总占用行数应为3行（表头+数据）")
	// 验证数据行数：应为1行（子表头行已正确合并到表头）
	assert.Equal(t, float64(1), dataRowCount, "数据行应为1行（子表头不应出现在数据行中）")
}

func TestHTMLTable_ParseSimpleTable(t *testing.T) {
	// 杆塔使用条件一览表 — 简单单行表头
	htmlInput := `<table><tr><td>塔型</td><td>呼高(m)</td><td>水平档距(m)</td><td>垂直档距(m)</td><td>转角度数(°)</td><td>覆冰(mm)</td><td>导线型号</td><td>地线型号</td></tr><tr><td>110-DD21GS-J4</td><td>18</td><td>150</td><td>200</td><td>0-90</td><td>导5地10</td><td>JL3/G1A-300/25</td><td>OPGW-13-90-2</td></tr></table>`

	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local result = htmltable.parse_html(%q)
local h = result.headers
local rows = result.rows
return {headers = h, row_count = result.row_count, data_row_count = result.data_row_count, col_count = result.col_count}
`, htmlInput),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	require.Equal(t, "success", resp["status"])

	r := resp["result"].(map[string]any)
	headers, _ := r["headers"].([]any)
	t.Logf("  headers = %v", headers)
	rowCount, _ := r["row_count"].(float64)
	t.Logf("  row_count (total) = %v", rowCount)
	dataRowCount, _ := r["data_row_count"].(float64)
	t.Logf("  data_row_count = %v", dataRowCount)

	// 总占用行数应为2行（1行表头 + 1行数据）
	assert.Equal(t, float64(2), rowCount, "总占用行数应为2行（表头+数据）")
	// 数据行应为1行
	assert.Equal(t, float64(1), dataRowCount, "数据行应为1行")
	assert.Equal(t, 8, len(headers), "表头应为8列")
}

func TestHTMLTable_ParseWithExplicitThead(t *testing.T) {
	// 显式使用 <thead> 的表格
	htmlInput := `<table><thead><tr><th rowspan="2">A</th><th>B1</th></tr><tr><th>B2</th></tr></thead><tbody><tr><td>1</td><td>2</td></tr></tbody></table>`

	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": fmt.Sprintf(`
local result = htmltable.parse_html(%q)
local h = result.headers
local rows = result.rows
return {headers = h, row_count = result.row_count, data_row_count = result.data_row_count, col_count = result.col_count}
`, htmlInput),
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	require.Equal(t, "success", resp["status"])

	r := resp["result"].(map[string]any)
	headers, _ := r["headers"].([]any)
	t.Logf("  headers = %v", headers)
	rowCount, _ := r["row_count"].(float64)
	t.Logf("  row_count (total) = %v", rowCount)
	dataRowCount, _ := r["data_row_count"].(float64)
	t.Logf("  data_row_count = %v", dataRowCount)

	// 显式 <thead> 的表格，多行表头应被正确合并
	if len(headers) >= 2 {
		assert.Contains(t, headers[1].(string), ">", "列2应包含 > 符号（子表头合并）")
	}

	// 总占用行数应为3行（2行表头 + 1行数据）
	assert.Equal(t, float64(3), rowCount, "总占用行数应为3行（表头+数据）")
	// 数据行应为1行
	assert.Equal(t, float64(1), dataRowCount, "数据行应为1行")
}

func TestHTML_FindWithAttrs(t *testing.T) {
	ts, err := NewToolSet(WithTools(&mockTool{name: "test_tool"}))
	require.NoError(t, err)
	defer ts.Close()

	ct := ts.Tools(context.Background())[0].(interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	})

	args, _ := json.Marshal(map[string]any{
		"script": `
local doc = html.parse("<div class='a'>A</div><div class='b'>B</div><div class='a'>C</div>")
local divs = html.find_all(doc, "div", {class = "a"})
return #divs
`,
	})
	result, err := ct.Call(context.Background(), args)
	require.NoError(t, err)
	resp := result.(map[string]any)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, float64(2), resp["result"])
}
