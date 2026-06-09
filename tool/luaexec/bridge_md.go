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
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	lua "github.com/yuin/gopher-lua"
)

const mdDocumentTypeName = "md_document"

// mdDocument wraps a parsed Markdown document with its source.
type mdDocument struct {
	root   ast.Node
	source []byte
}

// mdTableResult represents a parsed Markdown table for Lua conversion.
type mdTableResult struct {
	Headers    []string
	Rows       [][]string
	Alignments []string
	StartLine  int
	EndLine    int
}

// registerMDBridge registers the md module in the Lua VM.
// API aligns with Python markdown library for LLM familiarity.
func registerMDBridge(L *lua.LState) {
	mt := L.NewTypeMetatable(mdDocumentTypeName)
	L.SetGlobal(mdDocumentTypeName, mt)

	mod := L.NewTable()
	L.SetField(mod, "parse", L.NewFunction(bridgeMDParse))
	L.SetField(mod, "extract_tables", L.NewFunction(bridgeMDExtractTables))
	L.SetField(mod, "parse_table", L.NewFunction(bridgeMDParseTable))
	L.SetField(mod, "detect_merge", L.NewFunction(bridgeMDDetectMerge))
	L.SetGlobal("md", mod)
}

// bridgeMDParse implements md.parse(md_string) -> document.
// Parses Markdown with table extension enabled.
func bridgeMDParse(L *lua.LState) int {
	input := L.CheckString(1)

	md := goldmark.New(goldmark.WithExtensions(extension.Table))
	source := []byte(input)
	reader := text.NewReader(source)
	doc := md.Parser().Parse(reader)

	ud := L.NewUserData()
	ud.Value = &mdDocument{root: doc, source: source}
	L.SetMetatable(ud, L.GetTypeMetatable(mdDocumentTypeName))
	L.Push(ud)
	return 1
}

// bridgeMDExtractTables implements md.extract_tables(document_or_string) -> array.
// Returns an array of table objects with headers, rows, alignments, line ranges.
// Accepts either an md document userdata or a raw markdown string.
func bridgeMDExtractTables(L *lua.LState) int {
	var doc *mdDocument

	// Accept either userdata (from md.parse) or string (auto-parse).
	switch v := L.Get(1).(type) {
	case *lua.LUserData:
		var ok bool
		doc, ok = v.Value.(*mdDocument)
		if !ok {
			pushBridgeError(L, "md.extract_tables: expected md document or string")
			return 2
		}
	case lua.LString:
		md := goldmark.New(goldmark.WithExtensions(extension.Table))
		source := []byte(string(v))
		reader := text.NewReader(source)
		root := md.Parser().Parse(reader)
		doc = &mdDocument{root: root, source: source}
	default:
		pushBridgeError(L, "md.extract_tables: expected md document or string")
		return 2
	}

	var tables []mdTableResult
	ast.Walk(doc.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if tableNode, ok := n.(*extast.Table); ok {
			t := extractTableFromAST(tableNode, doc.source)
			tables = append(tables, t)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})

	resultTbl := L.NewTable()
	for i, t := range tables {
		tbl := L.NewTable()
		L.SetField(tbl, "headers", stringSliceToLTable(L, t.Headers))
		L.SetField(tbl, "rows", stringGridToLTable(L, t.Rows))
		L.SetField(tbl, "alignments", stringSliceToLTable(L, t.Alignments))
		L.SetField(tbl, "start_line", lua.LNumber(t.StartLine))
		L.SetField(tbl, "end_line", lua.LNumber(t.EndLine))
		L.RawSetInt(resultTbl, i+1, tbl)
	}
	L.Push(resultTbl)
	return 1
}

// bridgeMDParseTable implements md.parse_table(table_text [, index]) -> table_obj.
// Parses Markdown text and returns the Nth table (1-based index, default 1).
func bridgeMDParseTable(L *lua.LState) int {
	input := L.CheckString(1)
	index := L.OptInt(2, 1) // 1-based, default first table

	md := goldmark.New(goldmark.WithExtensions(extension.Table))
	source := []byte(input)
	reader := text.NewReader(source)
	doc := md.Parser().Parse(reader)

	var tables []*extast.Table
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if t, ok := n.(*extast.Table); ok {
			tables = append(tables, t)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})

	if index < 1 || index > len(tables) {
		pushBridgeError(L, fmt.Sprintf("md.parse_table: index %d out of range (found %d tables)", index, len(tables)))
		return 2
	}

	tableNode := tables[index-1]
	t := extractTableFromAST(tableNode, source)
	tbl := L.NewTable()
	L.SetField(tbl, "headers", stringSliceToLTable(L, t.Headers))
	L.SetField(tbl, "rows", stringGridToLTable(L, t.Rows))
	L.SetField(tbl, "alignments", stringSliceToLTable(L, t.Alignments))
	L.Push(tbl)
	return 1
}

// bridgeMDDetectMerge implements md.detect_merge(table_obj_or_string) -> merge_info.
// Detects potential merged cell patterns in a Markdown table.
// Accepts either a table object (from parse_table) or a raw markdown string.
// Markdown has no native merge syntax, so we detect patterns like
// empty cells that might indicate a merged cell from a visual layout.
func bridgeMDDetectMerge(L *lua.LState) int {
	var headers []string
	var rows [][]string

	switch v := L.Get(1).(type) {
	case *lua.LTable:
		headers = lTableToStringSlice(L, L.GetField(v, "headers"))
		rows = lTableToStringGrid(L, L.GetField(v, "rows"))
	case lua.LString:
		// Auto-parse the string as a markdown table.
		input := string(v)
		md := goldmark.New(goldmark.WithExtensions(extension.Table))
		source := []byte(input)
		reader := text.NewReader(source)
		doc := md.Parser().Parse(reader)
		var tableNode *extast.Table
		ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			if t, ok := n.(*extast.Table); ok {
				tableNode = t
				return ast.WalkStop, nil
			}
			return ast.WalkContinue, nil
		})
		if tableNode == nil {
			pushBridgeError(L, "md.detect_merge: no table found in input string")
			return 2
		}
		t := extractTableFromAST(tableNode, source)
		headers = t.Headers
		rows = t.Rows
	default:
		pushBridgeError(L, "md.detect_merge: expected table object or string")
		return 2
	}

	hasMerge := false
	var hints []map[string]any

	// Detect: consecutive empty cells in the same column across rows
	// might indicate a vertically merged cell.
	if len(rows) > 1 {
		maxCols := len(headers)
		for _, row := range rows {
			if len(row) > maxCols {
				maxCols = len(row)
			}
		}
		for col := 0; col < maxCols; col++ {
			emptyStreak := 0
			for row := 1; row < len(rows); row++ {
				if col < len(rows[row]) && rows[row][col] == "" {
					emptyStreak++
				} else {
					if emptyStreak > 0 {
						hints = append(hints, map[string]any{
							"type":      "vertical_merge_hint",
							"col":       col,
							"start_row": row - emptyStreak,
							"span":      emptyStreak + 1,
						})
						hasMerge = true
					}
					emptyStreak = 0
				}
			}
			if emptyStreak > 0 {
				hints = append(hints, map[string]any{
					"type":      "vertical_merge_hint",
					"col":       col,
					"start_row": len(rows) - emptyStreak,
					"span":      emptyStreak + 1,
				})
				hasMerge = true
			}
		}
	}

	result := L.NewTable()
	L.SetField(result, "has_merge", lua.LBool(hasMerge))
	hintsTbl := L.NewTable()
	for i, h := range hints {
		t := L.NewTable()
		for k, v := range h {
			switch val := v.(type) {
			case string:
				L.SetField(t, k, lua.LString(val))
			case int:
				L.SetField(t, k, lua.LNumber(val))
			case bool:
				L.SetField(t, k, lua.LBool(val))
			}
		}
		L.RawSetInt(hintsTbl, i+1, t)
	}
	L.SetField(result, "merge_hints", hintsTbl)
	L.Push(result)
	return 1
}

// extractTableFromAST walks a goldmark Table AST node and extracts structured data.
func extractTableFromAST(tableNode *extast.Table, source []byte) mdTableResult {
	result := mdTableResult{
		StartLine: 0,
		EndLine:   0,
	}

	// Extract alignments from column definitions.
	for _, col := range tableNode.Alignments {
		switch col {
		case extast.AlignLeft:
			result.Alignments = append(result.Alignments, "left")
		case extast.AlignRight:
			result.Alignments = append(result.Alignments, "right")
		case extast.AlignCenter:
			result.Alignments = append(result.Alignments, "center")
		default:
			result.Alignments = append(result.Alignments, "default")
		}
	}

	// Walk children for headers and rows.
	for child := tableNode.FirstChild(); child != nil; child = child.NextSibling() {
		switch v := child.(type) {
		case *extast.TableHeader:
			result.Headers = extractRowCells(v, source)
			if v.Lines().Len() > 0 {
				result.StartLine = v.Lines().At(0).Start
			}
		case *extast.TableRow:
			row := extractRowCells(v, source)
			result.Rows = append(result.Rows, row)
			if v.Lines().Len() > 0 {
				result.EndLine = v.Lines().At(v.Lines().Len() - 1).Stop
			}
		}
	}

	return result
}

// extractRowCells extracts cell text values from a table row or header.
// Goldmark stores cell content in child ast.Text nodes; Lines() may be empty.
func extractRowCells(row ast.Node, source []byte) []string {
	var cells []string
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		tc, ok := cell.(*extast.TableCell)
		if !ok {
			continue
		}
		var sb strings.Builder
		// Primary: extract from child text nodes via their Segments.
		for c := tc.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				seg := t.Segment
				sb.Write(source[seg.Start:seg.Stop])
			}
		}
		// Fallback: extract from Lines() segments if no child text found.
		if sb.Len() == 0 && tc.Lines().Len() > 0 {
			for i := 0; i < tc.Lines().Len(); i++ {
				seg := tc.Lines().At(i)
				sb.Write(source[seg.Start:seg.Stop])
			}
		}
		cells = append(cells, strings.TrimSpace(sb.String()))
	}
	return cells
}

// stringSliceToLTable converts a []string to a Lua array table.
func stringSliceToLTable(L *lua.LState, items []string) *lua.LTable {
	tbl := L.NewTable()
	for i, s := range items {
		L.RawSetInt(tbl, i+1, lua.LString(s))
	}
	return tbl
}

// stringGridToLTable converts a [][]string to a Lua array-of-arrays table.
func stringGridToLTable(L *lua.LState, grid [][]string) *lua.LTable {
	tbl := L.NewTable()
	for i, row := range grid {
		rowTbl := L.NewTable()
		for j, s := range row {
			L.RawSetInt(rowTbl, j+1, lua.LString(s))
		}
		L.RawSetInt(tbl, i+1, rowTbl)
	}
	return tbl
}

// lTableToStringSlice converts a Lua array table to []string.
func lTableToStringSlice(L *lua.LState, v lua.LValue) []string {
	if v == lua.LNil {
		return nil
	}
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return nil
	}
	var result []string
	n := tbl.MaxN()
	for i := 1; i <= n; i++ {
		result = append(result, lua.LVAsString(tbl.RawGetInt(i)))
	}
	return result
}

// lTableToStringGrid converts a Lua array-of-arrays table to [][]string.
func lTableToStringGrid(L *lua.LState, v lua.LValue) [][]string {
	if v == lua.LNil {
		return nil
	}
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return nil
	}
	var result [][]string
	n := tbl.MaxN()
	for i := 1; i <= n; i++ {
		result = append(result, lTableToStringSlice(L, tbl.RawGetInt(i)))
	}
	return result
}
