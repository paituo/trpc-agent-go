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
