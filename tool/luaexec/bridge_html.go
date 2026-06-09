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

	"golang.org/x/net/html"
	lua "github.com/yuin/gopher-lua"
)

const htmlElementTypeName = "html_element"
const htmlDocumentTypeName = "html_document"

// htmlElement wraps an *html.Node for use as Lua userdata.
type htmlElement struct {
	node   *html.Node
	source []byte
}

// htmlDocument wraps a parsed HTML document with its source.
type htmlDocument struct {
	root   *html.Node
	source []byte
}

// registerHTMLBridge registers the html module in the Lua VM.
// API aligns with Python BeautifulSoup for LLM familiarity.
// Supports both function-style (html.find(doc, "h1")) and method-style (doc:find("h1")).
func registerHTMLBridge(L *lua.LState) {
	// Register metatable for htmlDocument with __index for method-style calls.
	docMT := L.NewTypeMetatable(htmlDocumentTypeName)
	L.SetGlobal(htmlDocumentTypeName, docMT)
	L.SetField(docMT, "__index", L.NewFunction(func(L *lua.LState) int {
		// __index(self, key) — delegate to html module table.
		key := L.CheckString(2)
		mod := L.GetGlobal("html")
		if fn := L.GetField(mod, key); fn != lua.LNil {
			L.Push(fn)
			return 1
		}
		L.Push(lua.LNil)
		return 1
	}))

	// Register metatable for htmlElement with __index for method-style calls.
	elemMT := L.NewTypeMetatable(htmlElementTypeName)
	L.SetGlobal(htmlElementTypeName, elemMT)
	L.SetField(elemMT, "__index", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(2)
		mod := L.GetGlobal("html")
		if fn := L.GetField(mod, key); fn != lua.LNil {
			L.Push(fn)
			return 1
		}
		L.Push(lua.LNil)
		return 1
	}))

	mod := L.NewTable()
	L.SetField(mod, "parse", L.NewFunction(bridgeHTMLParse))
	L.SetField(mod, "find", L.NewFunction(bridgeHTMLFind))
	L.SetField(mod, "find_all", L.NewFunction(bridgeHTMLFindAll))
	L.SetField(mod, "select", L.NewFunction(bridgeHTMLSelect))
	L.SetField(mod, "select_all", L.NewFunction(bridgeHTMLSelectAll))
	L.SetField(mod, "get_text", L.NewFunction(bridgeHTMLGetText))
	L.SetField(mod, "get_attr", L.NewFunction(bridgeHTMLGetAttr))
	L.SetField(mod, "children", L.NewFunction(bridgeHTMLChildren))
	L.SetField(mod, "all_children", L.NewFunction(bridgeHTMLAllChildren))
	L.SetField(mod, "tag_name", L.NewFunction(bridgeHTMLTagName))
	L.SetField(mod, "parent", L.NewFunction(bridgeHTMLParent))
	L.SetGlobal("html", mod)
}

// bridgeHTMLParse implements html.parse(html_string) -> document.
// golang.org/x/net/html is an HTML5-compliant parser that auto-closes
// tags and handles malformed input with strong fault tolerance.
func bridgeHTMLParse(L *lua.LState) int {
	input := L.CheckString(1)

	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		pushBridgeError(L, fmt.Sprintf("html.parse failed: %v", err))
		return 2
	}

	ud := L.NewUserData()
	ud.Value = &htmlDocument{root: doc, source: []byte(input)}
	L.SetMetatable(ud, L.GetTypeMetatable(htmlDocumentTypeName))
	L.Push(ud)
	return 1
}

// bridgeHTMLFind implements html.find(element, tag [, attrs]) -> element|nil.
// Finds the first descendant matching tag name and optional attributes.
// attrs is an optional table of attribute key-value pairs.
// Supports method-style: elem:find("p", {class="main"})
func bridgeHTMLFind(L *lua.LState) int {
	parent := checkHTMLElement(L, 1)
	tag := L.CheckString(2)
	var attrs map[string]string
	if L.GetTop() >= 3 {
		if tbl, ok := L.Get(3).(*lua.LTable); ok {
			attrs = luaTableToStringMap(tbl)
		}
	}

	result := findFirst(parent.node, tag, attrs)
	if result == nil {
		L.Push(lua.LNil)
		return 1
	}

	pushHTMLElement(L, result, parent.source)
	return 1
}

// bridgeHTMLFindAll implements html.find_all(element, tag [, attrs]) -> array.
func bridgeHTMLFindAll(L *lua.LState) int {
	parent := checkHTMLElement(L, 1)
	tag := L.CheckString(2)
	var attrs map[string]string
	if L.GetTop() >= 3 {
		if tbl, ok := L.Get(3).(*lua.LTable); ok {
			attrs = luaTableToStringMap(tbl)
		}
	}

	results := findAll(parent.node, tag, attrs)
	tbl := L.NewTable()
	for i, node := range results {
		pushHTMLElement(L, node, parent.source)
		L.RawSetInt(tbl, i+1, L.Get(-1))
		L.Pop(1)
	}
	L.Push(tbl)
	return 1
}

// bridgeHTMLSelect implements html.select(element, selector) -> element|nil.
// Supports simple CSS selectors: #id, .class, tag, [attr], tag.class, tag#id, tag[attr].
func bridgeHTMLSelect(L *lua.LState) int {
	parent := checkHTMLElement(L, 1)
	selector := L.CheckString(2)

	sel := parseSimpleSelector(selector)
	result := findFirstBySelector(parent.node, sel)
	if result == nil {
		L.Push(lua.LNil)
		return 1
	}

	pushHTMLElement(L, result, parent.source)
	return 1
}

// bridgeHTMLSelectAll implements html.select_all(element, selector) -> array.
func bridgeHTMLSelectAll(L *lua.LState) int {
	parent := checkHTMLElement(L, 1)
	selector := L.CheckString(2)

	sel := parseSimpleSelector(selector)
	results := findAllBySelector(parent.node, sel)
	tbl := L.NewTable()
	for i, node := range results {
		pushHTMLElement(L, node, parent.source)
		L.RawSetInt(tbl, i+1, L.Get(-1))
		L.Pop(1)
	}
	L.Push(tbl)
	return 1
}

// bridgeHTMLGetText implements html.get_text(element [, separator]) -> string.
// Returns all text content within the element, recursively.
// separator defaults to " " (space) to distinguish text boundaries.
func bridgeHTMLGetText(L *lua.LState) int {
	elem := checkHTMLElement(L, 1)
	separator := L.OptString(2, " ")
	text := extractTextWithSeparator(elem.node, separator)
	L.Push(lua.LString(text))
	return 1
}

// bridgeHTMLGetAttr implements html.get_attr(element, attr_name) -> string|nil.
func bridgeHTMLGetAttr(L *lua.LState) int {
	elem := checkHTMLElement(L, 1)
	attrName := L.CheckString(2)
	for _, a := range elem.node.Attr {
		if a.Key == attrName {
			L.Push(lua.LString(a.Val))
			return 1
		}
	}
	L.Push(lua.LNil)
	return 1
}

// bridgeHTMLChildren implements html.children(element) -> array.
// Returns only ElementNode children (same as BeautifulSoup .children).
func bridgeHTMLChildren(L *lua.LState) int {
	elem := checkHTMLElement(L, 1)
	tbl := L.NewTable()
	i := 1
	for c := elem.node.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			pushHTMLElement(L, c, elem.source)
			L.RawSetInt(tbl, i, L.Get(-1))
			L.Pop(1)
			i++
		}
	}
	L.Push(tbl)
	return 1
}

// bridgeHTMLAllChildren implements html.all_children(element) -> array.
// Returns all children including text nodes.
// Text nodes are returned as plain strings, element nodes as html_element userdata.
func bridgeHTMLAllChildren(L *lua.LState) int {
	elem := checkHTMLElement(L, 1)
	tbl := L.NewTable()
	i := 1
	for c := elem.node.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.ElementNode:
			pushHTMLElement(L, c, elem.source)
			L.RawSetInt(tbl, i, L.Get(-1))
			L.Pop(1)
			i++
		case html.TextNode:
			text := strings.TrimSpace(c.Data)
			if text != "" {
				L.RawSetInt(tbl, i, lua.LString(c.Data))
				i++
			}
		}
	}
	L.Push(tbl)
	return 1
}

// bridgeHTMLTagName implements html.tag_name(element) -> string.
func bridgeHTMLTagName(L *lua.LState) int {
	elem := checkHTMLElement(L, 1)
	if elem.node.Type == html.ElementNode {
		L.Push(lua.LString(elem.node.Data))
	} else {
		L.Push(lua.LString(""))
	}
	return 1
}

// bridgeHTMLParent implements html.parent(element) -> element|nil.
func bridgeHTMLParent(L *lua.LState) int {
	elem := checkHTMLElement(L, 1)
	if elem.node.Parent == nil {
		L.Push(lua.LNil)
		return 1
	}
	pushHTMLElement(L, elem.node.Parent, elem.source)
	return 1
}

// pushHTMLElement pushes an htmlElement userdata onto the Lua stack.
func pushHTMLElement(L *lua.LState, node *html.Node, source []byte) {
	ud := L.NewUserData()
	ud.Value = &htmlElement{node: node, source: source}
	L.SetMetatable(ud, L.GetTypeMetatable(htmlElementTypeName))
	L.Push(ud)
}

// checkHTMLElement accepts either an htmlDocument or htmlElement as the parent.
func checkHTMLElement(L *lua.LState, n int) *htmlElement {
	ud := L.CheckUserData(n)
	switch v := ud.Value.(type) {
	case *htmlDocument:
		return &htmlElement{node: v.root, source: v.source}
	case *htmlElement:
		return v
	default:
		L.ArgError(n, "expected html element or document")
		return nil
	}
}

// cssSelector represents a parsed simple CSS selector.
type cssSelector struct {
	tag      string // tag name, empty if not specified
	id       string // #id value, empty if not specified
	class    string // .class value, empty if not specified
	attr     string // [attr] name, empty if not specified
	attrVal  string // [attr=val] value, empty if not specified
}

// parseSimpleSelector parses a simple CSS selector string.
// Supported: #id, .class, tag, [attr], [attr=val], tag.class, tag#id, tag[attr]
func parseSimpleSelector(s string) cssSelector {
	var sel cssSelector
	remaining := s

	// Extract [attr] or [attr=val] suffix.
	if idx := strings.Index(remaining, "["); idx >= 0 {
		end := strings.Index(remaining[idx:], "]")
		if end >= 0 {
			attrPart := remaining[idx+1 : idx+end]
			if eq := strings.Index(attrPart, "="); eq >= 0 {
				sel.attr = attrPart[:eq]
				sel.attrVal = strings.Trim(attrPart[eq+1:], `"'`)
			} else {
				sel.attr = attrPart
			}
			remaining = remaining[:idx]
		}
	}

	// Extract #id.
	if idx := strings.Index(remaining, "#"); idx >= 0 {
		sel.id = remaining[idx+1:]
		remaining = remaining[:idx]
	}

	// Extract .class.
	if idx := strings.Index(remaining, "."); idx >= 0 {
		sel.class = remaining[idx+1:]
		remaining = remaining[:idx]
	}

	// Remaining is the tag name.
	sel.tag = remaining

	return sel
}

// matchesSelector checks if an HTML node matches a CSS selector.
func matchesSelector(n *html.Node, sel cssSelector) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if sel.tag != "" && n.Data != sel.tag {
		return false
	}
	if sel.id != "" {
		found := false
		for _, a := range n.Attr {
			if a.Key == "id" && a.Val == sel.id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if sel.class != "" {
		found := false
		for _, a := range n.Attr {
			if a.Key == "class" && strings.Contains(a.Val, sel.class) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if sel.attr != "" {
		found := false
		for _, a := range n.Attr {
			if a.Key == sel.attr {
				if sel.attrVal == "" || a.Val == sel.attrVal {
					found = true
				}
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// findFirstBySelector finds the first descendant matching a CSS selector.
func findFirstBySelector(n *html.Node, sel cssSelector) *html.Node {
	if matchesSelector(n, sel) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findFirstBySelector(c, sel); result != nil {
			return result
		}
	}
	return nil
}

// findAllBySelector collects all descendants matching a CSS selector.
func findAllBySelector(n *html.Node, sel cssSelector) []*html.Node {
	var results []*html.Node
	collectAllBySelector(n, sel, &results)
	return results
}

func collectAllBySelector(n *html.Node, sel cssSelector, results *[]*html.Node) {
	if matchesSelector(n, sel) {
		*results = append(*results, n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectAllBySelector(c, sel, results)
	}
}

// findFirst performs a depth-first search for the first matching node.
func findFirst(n *html.Node, tag string, attrs map[string]string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag && matchAttrs(n, attrs) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findFirst(c, tag, attrs); result != nil {
			return result
		}
	}
	return nil
}

// findAll collects all matching nodes via depth-first traversal.
func findAll(n *html.Node, tag string, attrs map[string]string) []*html.Node {
	var results []*html.Node
	collectAll(n, tag, attrs, &results)
	return results
}

func collectAll(n *html.Node, tag string, attrs map[string]string, results *[]*html.Node) {
	if n.Type == html.ElementNode && n.Data == tag && matchAttrs(n, attrs) {
		*results = append(*results, n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectAll(c, tag, attrs, results)
	}
}

// matchAttrs checks if a node has all specified attributes.
func matchAttrs(n *html.Node, attrs map[string]string) bool {
	if len(attrs) == 0 {
		return true
	}
	nodeAttrs := make(map[string]string, len(n.Attr))
	for _, a := range n.Attr {
		nodeAttrs[a.Key] = a.Val
	}
	for k, v := range attrs {
		if nodeAttrs[k] != v {
			return false
		}
	}
	return true
}

// extractText recursively extracts text content from an HTML node.
func extractText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(extractText(c))
	}
	return sb.String()
}

// extractTextWithSeparator recursively extracts text with a separator between children.
func extractTextWithSeparator(n *html.Node, sep string) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var parts []string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		t := extractTextWithSeparator(c, sep)
		t = strings.TrimSpace(t)
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, sep)
}

// luaTableToStringMap converts a Lua table to map[string]string.
func luaTableToStringMap(tbl *lua.LTable) map[string]string {
	result := make(map[string]string)
	tbl.ForEach(func(k, v lua.LValue) {
		key := luaValueToString(k)
		val := luaValueToString(v)
		result[key] = val
	})
	return result
}
