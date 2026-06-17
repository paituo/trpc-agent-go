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
)

// TestScriptCompat_StripMarkdownFormatting 测试去除Markdown格式标记（脚本中 strip_markdown_formatting 函数）
func TestScriptCompat_StripMarkdownFormatting(t *testing.T) {
	script := `
local text = "**粗体文本**和*斜体*和\96代码\96"
text = utf8.gsub(text, "\\*\\*(.+?)\\*\\*", "${1}")
text = utf8.gsub(text, "\\*(.+?)\\*", "${1}")
text = utf8.gsub(text, "\96(.+?)\96", "${1}")
return text
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "粗体文本和斜体和代码", resp["result"])
}

// TestScriptCompat_ParseHeading 测试解析Markdown标题（脚本中 parse_heading 函数）
func TestScriptCompat_ParseHeading(t *testing.T) {
	script := `
local _, hashes, text = utf8.find("## 工程概况", "^(#{1,6})\\s+(.+)$")
return hashes .. "|" .. text
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "##|工程概况", resp["result"])
}

// TestScriptCompat_ParseChineseChapter 测试解析中文章节标题
func TestScriptCompat_ParseChineseChapter(t *testing.T) {
	script := `
local _, chapter_text = utf8.find("  第3章 设计依据", "^\\s*第\\d+章[\\s　]+(.+)$")
if not chapter_text then return "no_match" end
return chapter_text
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "设计依据", resp["result"])
}

// TestScriptCompat_ParseNumberedHeading 测试解析数字编号标题
func TestScriptCompat_ParseNumberedHeading(t *testing.T) {
	script := `
local _, num_prefix, num_text = utf8.find("  1.1.1 设计范围", "^(\\s*\\d+(?:\\.\\d+)+)[\\s　]+(.+)$")
if not num_prefix then return "no_match" end
return num_prefix .. "|" .. num_text
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "  1.1.1|设计范围", resp["result"])
}

// TestScriptCompat_FindTableTitle 测试表格标题检测
func TestScriptCompat_FindTableTitle(t *testing.T) {
	script := `
-- 检测 "表 1-1 主要参数"
local r1 = utf8.find("表 1-1 主要参数", "^表\\s*\\d")
-- 检测 "表 一"（中文数字）
local r2 = utf8.find("表 一 参数", "^表\\s*第")
-- 检测不是表格标题的行
local r3 = utf8.find("普通文本行", "^表\\s*\\d")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil) .. "|" .. tostring(r3 ~= nil)
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false|false", resp["result"])
}

// TestScriptCompat_RemoveHashPrefix 测试去除标题#前缀
func TestScriptCompat_RemoveHashPrefix(t *testing.T) {
	script := `
local title_text = utf8.gsub("## 工程概况", "^#+\\s*", "")
return title_text
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "工程概况", resp["result"])
}

// TestScriptCompat_TableSeparator 测试表格分隔行检测
func TestScriptCompat_TableSeparator(t *testing.T) {
	script := `
local r1 = utf8.find("| --- | --- |", "^\\s*\\|[\\s\\-:|]+\\|\\s*$")
local r2 = utf8.find("| 列1 | 列2 |", "^\\s*\\|[\\s\\-:|]+\\|\\s*$")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil)
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false", resp["result"])
}

// TestScriptCompat_IsTocPageRef 测试目录页码引用检测
func TestScriptCompat_IsTocPageRef(t *testing.T) {
	script := `
local r1 = utf8.find("......12", "\\.{2,}\\d")
local r2 = utf8.find("这不是目录", "\\.{2,}\\d")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil)
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false", resp["result"])
}

// TestScriptCompat_ExtractImages 测试提取图片引用（使用 utf8.matches）
func TestScriptCompat_ExtractImages(t *testing.T) {
	script := `
local line = "![图片1](url1.png) 文本 ![图片2](url2.png)"
local all_matches = utf8.matches(line, "!\\[([^\\]]*)\\]\\(([^\\)]+)\\)")
if not all_matches or #all_matches == 0 then return "no_match" end
local result = ""
for _, m in ipairs(all_matches) do
    if result ~= "" then result = result .. ";" end
    result = result .. (m[2] or "") .. "=" .. (m[3] or "")
end
return result
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "图片1=url1.png;图片2=url2.png", resp["result"])
}

// TestScriptCompat_ExtractImages_NormalText 测试无图片的行
func TestScriptCompat_ExtractImages_NormalText(t *testing.T) {
	script := `
local line = "这是一段普通文本"
local all_matches = utf8.matches(line, "!\\[([^\\]]*)\\]\\(([^\\)]+)\\)")
return tostring(all_matches ~= nil) .. "|" .. #all_matches
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|0", resp["result"])
}

// TestScriptCompat_StripHtmlTags 测试去除HTML标签
func TestScriptCompat_StripHtmlTags(t *testing.T) {
	script := `
local text = "<p>带HTML标签的<b>中文</b>文本</p>"
text = utf8.gsub(text, "<[^>]+>", "")
return text
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "带HTML标签的中文文本", resp["result"])
}

// TestScriptCompat_MdTableStart 测试MD表格行检测
func TestScriptCompat_MdTableStart(t *testing.T) {
	script := `
local r1 = utf8.find("| 列1 | 列2 |", "^\\|")
local r2 = utf8.find("普通文本", "^\\|")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil)
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false", resp["result"])
}

// TestScriptCompat_HeadingDetection 测试标题检测
func TestScriptCompat_HeadingDetection(t *testing.T) {
	script := `
local r1 = utf8.find("### 3.1 设计范围", "^#{1,6}\\s")
local r2 = utf8.find("普通段落文本", "^#{1,6}\\s")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil)
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false", resp["result"])
}

// TestScriptCompat_TocLinkLine 测试TOC链接行检测
func TestScriptCompat_TocLinkLine(t *testing.T) {
	script := `
local r1 = utf8.find("  - [1.1 设计依据](#design)", "^\\s*-\\s*\\[\\d[\\d.]*\\s")
local r2 = utf8.find("- 普通列表项", "^\\s*-\\s*\\[\\d[\\d.]*\\s")
return tostring(r1 ~= nil) .. "|" .. tostring(r2 ~= nil)
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "true|false", resp["result"])
}

// TestScriptCompat_ChineseFullScenario 测试完整中文场景
func TestScriptCompat_ChineseFullScenario(t *testing.T) {
	script := `
-- 模拟 strip_markdown_formatting 处理中文
local title = "**第1章 总则（工程概况）**"
title = utf8.gsub(title, "\\*\\*(.+?)\\*\\*", "${1}")
title = utf8.gsub(title, "\96(.+?)\96", "${1}")

-- 模拟 parse_heading 处理
local _, hashes, text = utf8.find("# 设计依据", "^(#{1,6})\\s+(.+)$")
local level = hashes and #hashes or 0

-- 模拟 find_table_title 中的表格检测
local table_ref = utf8.find("表 1.1-1 主要技术参数", "^表\\s*\\d")

-- 模拟 is_toc_title 检测
local toc_clean = utf8.gsub("**目  录**", "\\*\\*(.+?)\\*\\*", "${1}")

return title .. "|" .. text .. "|" .. level .. "|" .. tostring(table_ref ~= nil) .. "|" .. toc_clean
`
	resp := runLuaScript(t, script)
	assert.Equal(t, "success", resp["status"])
	assert.Equal(t, "第1章 总则（工程概况）|设计依据|1|true|目  录", resp["result"])
}