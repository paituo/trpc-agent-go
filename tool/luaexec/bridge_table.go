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
	"strconv"
	"strings"

	"golang.org/x/net/html"
	lua "github.com/yuin/gopher-lua"
)

// cell represents a single cell in the table grid, tracking merge info.
type cell struct {
	value    string
	rowspan  int
	colspan  int
	isOrigin bool   // true if this is the top-left origin of a merge region
	origin   string // value from the origin cell (for merged/occupied cells)
}

// nestedTableInfo holds a nested sub-table found inside a cell.
type nestedTableInfo struct {
	row       int
	col       int
	subResult map[string]any // same structure as top-level result
}

// registerTableBridge registers the htmltable module in the Lua VM.
// Named "htmltable" to avoid conflict with Lua's built-in "table" library.
func registerTableBridge(L *lua.LState) {
	mod := L.NewTable()
	L.SetField(mod, "parse_html", L.NewFunction(bridgeTableParseHTML))
	L.SetGlobal("htmltable", mod)
}

// bridgeTableParseHTML implements htmltable.parse_html(html_string) -> table.
// Parses an HTML string and returns structured table data including headers,
// rows, merge hints, nested tables, and a preview string.
func bridgeTableParseHTML(L *lua.LState) int {
	input := L.CheckString(1)

	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		result := makeErrorResult(fmt.Sprintf("HTML解析失败: %v", err))
		pushGoValue(L, result)
		return 1
	}

	// Find the first <table> node.
	tableNode := findFirstNode(doc, "table")
	if tableNode == nil {
		result := makeErrorResult("未找到<table>标签")
		pushGoValue(L, result)
		return 1
	}

	result := parseTableNode(tableNode)
	pushGoValue(L, result)
	return 1
}

// parseTableNode parses a single <table> node into a structured result map.
func parseTableNode(tableNode *html.Node) map[string]any {
	// Collect all <tr> rows, distinguishing thead rows from body rows.
	theadRows, tbodyRows := collectTableRows(tableNode)

	// If no thead rows, treat the first tbody row as header.
	if len(theadRows) == 0 && len(tbodyRows) > 0 {
		theadRows = []*html.Node{tbodyRows[0]}
		tbodyRows = tbodyRows[1:]
	}

	// Build the grid from all rows (thead + tbody).
	allRows := make([]*html.Node, 0, len(theadRows)+len(tbodyRows))
	allRows = append(allRows, theadRows...)
	allRows = append(allRows, tbodyRows...)

	grid, mergeHints := buildGrid(allRows)

	colCount := 0
	if len(grid) > 0 {
		colCount = len(grid[0])
	}

	// Flatten multi-row headers with ">" separator.
	headers := flattenHeaders(grid, len(theadRows), colCount)

	// Extract data rows (rows after header rows).
	dataRows := extractDataRows(grid, len(theadRows), colCount)

	// Find nested tables.
	nestedTables := findNestedTables(tableNode, grid, len(theadRows))

	// Generate preview.
	preview := generatePreview(headers, dataRows)

	result := map[string]any{
		"headers":        headers,
		"rows":           dataRows,
		"preview":        preview,
		"row_count":      len(dataRows),
		"col_count":      colCount,
		"merge_hints":    mergeHints,
		"nested_tables":  nestedTables,
		"has_error":      false,
		"error_message":  "",
	}
	return result
}

// collectTableRows separates <tr> nodes inside <thead> and <tbody>/<tfoot>/<body>.
func collectTableRows(tableNode *html.Node) (theadRows, tbodyRows []*html.Node) {
	// First check for explicit <thead>.
	thead := findFirstNode(tableNode, "thead")
	if thead != nil {
		theadRows = findDirectTRs(thead)
	}

	// Collect rows from <tbody>, <tfoot>, or direct children.
	tbody := findFirstNode(tableNode, "tbody")
	if tbody != nil {
		tbodyRows = findDirectTRs(tbody)
	} else {
		// No <tbody>: collect direct <tr> children of <table>.
		tbodyRows = findDirectTRs(tableNode)
	}

	return theadRows, tbodyRows
}

// findDirectTRs finds all direct <tr> children of a node.
func findDirectTRs(n *html.Node) []*html.Node {
	var rows []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "tr" {
			rows = append(rows, c)
		}
	}
	return rows
}

// buildGrid constructs a 2D grid from <tr> nodes, handling rowspan/colspan.
// Cells that are spanned by a merge origin get the origin's value filled in.
func buildGrid(rows []*html.Node) ([][]cell, []map[string]any) {
	// First pass: determine grid dimensions by scanning all cells.
	maxCol := 0
	cellData := make([][]cellInfo, len(rows))

	for i, tr := range rows {
		cells := extractCells(tr)
		cellData[i] = cells
		colSum := 0
		for _, c := range cells {
			colSum += c.colspan
		}
		if colSum > maxCol {
			maxCol = colSum
		}
	}

	// Account for rowspan expanding the row count.
	totalRows := len(rows)
	// We'll expand as needed during grid placement.

	// Initialize grid with empty cells.
	grid := make([][]cell, totalRows)
	for i := range grid {
		grid[i] = make([]cell, maxCol)
	}

	var mergeHints []map[string]any

	// occupied tracks which grid positions are already filled by previous spans.
	occupied := make(map[[2]int]bool)

	for rowIdx, tr := range rows {
		cells := extractCells(tr)
		colIdx := 0

		for _, c := range cells {
			// Skip occupied positions (from previous rowspan/colspan).
			for occupied[[2]int{rowIdx, colIdx}] {
				colIdx++
			}

			rs := c.rowspan
			cs := c.colspan
			if rs < 1 {
				rs = 1
			}
			if cs < 1 {
				cs = 1
			}

			// Ensure grid has enough rows.
			neededRows := rowIdx + rs
			for len(grid) < neededRows {
				grid = append(grid, make([]cell, maxCol))
			}

			// Ensure grid has enough columns.
			neededCols := colIdx + cs
			if neededCols > maxCol {
				for r := range grid {
					for len(grid[r]) < neededCols {
						grid[r] = append(grid[r], cell{})
					}
				}
				maxCol = neededCols
			}

			// Fill the merge region.
			for dr := 0; dr < rs; dr++ {
				for dc := 0; dc < cs; dc++ {
					r := rowIdx + dr
					c2 := colIdx + dc
					grid[r][c2] = cell{
						value:    c.text,
						rowspan:  rs,
						colspan:  cs,
						isOrigin: dr == 0 && dc == 0,
						origin:   c.text,
					}
					occupied[[2]int{r, c2}] = true
				}
			}

			// Record merge hint if there's actual merging.
			if rs > 1 || cs > 1 {
				mergeHints = append(mergeHints, map[string]any{
					"row":     rowIdx,
					"col":     colIdx,
					"rowspan": rs,
					"colspan": cs,
					"value":   c.text,
				})
			}

			colIdx += cs
		}
	}

	return grid, mergeHints
}

// cellInfo holds extracted cell data from a <td> or <th>.
type cellInfo struct {
	text    string
	rowspan int
	colspan int
}

// extractCells extracts cell data from direct <td>/<th> children of a <tr>.
func extractCells(tr *html.Node) []cellInfo {
	var cells []cellInfo
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
			ci := cellInfo{
				text:    strings.TrimSpace(extractText(c)),
				rowspan: getAttrInt(c, "rowspan"),
				colspan: getAttrInt(c, "colspan"),
			}
			if ci.rowspan == 0 {
				ci.rowspan = 1
			}
			if ci.colspan == 0 {
				ci.colspan = 1
			}
			cells = append(cells, ci)
		}
	}
	return cells
}

// getAttrInt parses an HTML attribute as an integer, returning 0 if absent or invalid.
func getAttrInt(n *html.Node, attr string) int {
	for _, a := range n.Attr {
		if a.Key == attr {
			v, err := strconv.Atoi(a.Val)
			if err != nil {
				return 0
			}
			return v
		}
	}
	return 0
}

// flattenHeaders creates a flat header list from multi-row headers.
// Multi-row headers are joined with ">" (e.g., "基础>桩径(m)").
func flattenHeaders(grid [][]cell, headerRowCount, colCount int) []string {
	if headerRowCount == 0 || colCount == 0 {
		return []string{}
	}

	// For each column, collect header values from all header rows.
	headers := make([]string, colCount)
	for col := 0; col < colCount; col++ {
		var parts []string
		for row := 0; row < headerRowCount && row < len(grid); row++ {
			if col < len(grid[row]) {
				val := strings.TrimSpace(grid[row][col].value)
				if val != "" {
					// Avoid duplicate consecutive parts (from rowspan).
					if len(parts) == 0 || parts[len(parts)-1] != val {
						parts = append(parts, val)
					}
				}
			}
		}
		headers[col] = strings.Join(parts, ">")
	}
	return headers
}

// extractDataRows extracts data rows from the grid (rows after header rows).
func extractDataRows(grid [][]cell, headerRowCount, colCount int) [][]string {
	if headerRowCount >= len(grid) {
		return [][]string{}
	}

	rows := make([][]string, 0, len(grid)-headerRowCount)
	for r := headerRowCount; r < len(grid); r++ {
		row := make([]string, colCount)
		for c := 0; c < colCount && c < len(grid[r]); c++ {
			row[c] = grid[r][c].value
		}
		rows = append(rows, row)
	}
	return rows
}

// findNestedTables recursively finds <table> nodes inside <td>/<th> cells
// and parses them as nested sub-tables.
func findNestedTables(tableNode *html.Node, grid [][]cell, headerRowCount int) []map[string]any {
	var nested []map[string]any

	// Walk all <td>/<th> cells looking for inner <table>.
	rowIdx := 0
	var walkTRs func(n *html.Node)
	walkTRs = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			colIdx := 0
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
					innerTable := findFirstNode(c, "table")
					if innerTable != nil {
						subResult := parseTableNode(innerTable)
						nested = append(nested, map[string]any{
							"row":         rowIdx,
							"col":         colIdx,
							"sub_table":   subResult,
						})
					}
					colIdx++
				}
			}
			rowIdx++
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkTRs(c)
		}
	}

	// Walk within thead and tbody sections.
	for c := tableNode.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			walkTRs(c)
		}
	}

	return nested
}

// generatePreview creates a human-readable preview of the table.
// Shows header row + up to 5 data rows, with cells separated by "|".
func generatePreview(headers []string, dataRows [][]string) string {
	var lines []string

	if len(headers) > 0 {
		lines = append(lines, "表头: "+strings.Join(headers, "|"))
	}

	limit := 5
	if len(dataRows) < limit {
		limit = len(dataRows)
	}
	for i := 0; i < limit; i++ {
		lines = append(lines, strings.Join(dataRows[i], "|"))
	}

	if len(dataRows) > 5 {
		lines = append(lines, fmt.Sprintf("... 共%d行数据", len(dataRows)))
	}

	return strings.Join(lines, "\n")
}

// findFirstNode recursively finds the first descendant element node with the given tag.
func findFirstNode(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findFirstNode(c, tag); result != nil {
			return result
		}
	}
	return nil
}

// makeErrorResult creates a result map indicating a parse error.
func makeErrorResult(msg string) map[string]any {
	return map[string]any{
		"headers":       []string{},
		"rows":          [][]string{},
		"preview":       "",
		"row_count":     0,
		"col_count":     0,
		"merge_hints":   []map[string]any{},
		"nested_tables": []map[string]any{},
		"has_error":     true,
		"error_message": msg,
	}
}
