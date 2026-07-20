//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fragment

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
)

func writeMDFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err)
	return path
}

func TestGenerateNodeID(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		id1 := generateNodeID("fragment:h_10_1")
		id2 := generateNodeID("fragment:h_10_1")
		require.Equal(t, id1, id2)
	})

	t.Run("different input produces different output", func(t *testing.T) {
		id1 := generateNodeID("fragment:a")
		id2 := generateNodeID("fragment:b")
		require.NotEqual(t, id1, id2)
	})

	t.Run("contains node prefix", func(t *testing.T) {
		id := generateNodeID("fragment:test")
		require.True(t, strings.HasPrefix(id, "node:"))
	})

	t.Run("length is node: + 24 hex chars", func(t *testing.T) {
		id := generateNodeID("fragment:test")
		require.Equal(t, len("node:")+24, len(id))
	})
}

func TestCloneMap(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		require.Nil(t, cloneMap(nil))
	})

	t.Run("clones correctly", func(t *testing.T) {
		original := map[string]any{"a": 1, "b": "two"}
		cloned := cloneMap(original)
		require.Equal(t, original, cloned)
	})

	t.Run("mutation does not affect original", func(t *testing.T) {
		original := map[string]any{"key": "value"}
		cloned := cloneMap(original)
		cloned["key"] = "changed"
		cloned["extra"] = "new"
		require.Equal(t, "value", original["key"])
		_, exists := original["extra"]
		require.False(t, exists)
	})
}

func TestParseMDDocument(t *testing.T) {

	t.Run("simple L1 headings", func(t *testing.T) {
		dir := t.TempDir()
		path := writeMDFile(t, dir, "simple.md", "# Title A\nBody A\n# Title B\nBody B\n")
		frags, err := (&Source{logging: false}).parseMDDocument(path)
		require.NoError(t, err)
		// 顶层无子标题的标题在 5.4（口径 B）下建模为 chapter，
		// 正文移到其唯一的 body fragment 子节点中。
		require.Len(t, frags, 2)
		require.Equal(t, "Title A", frags[0].name)
		require.Equal(t, "Title B", frags[1].name)
		require.Len(t, frags[0].children, 1, "Title A chapter 应有 1 个 body fragment")
		require.Len(t, frags[1].children, 1, "Title B chapter 应有 1 个 body fragment")
		require.Contains(t, frags[0].children[0].content, "Body A")
		require.Contains(t, frags[1].children[0].content, "Body B")
		require.Equal(t, 1, frags[0].headingLevel)
		require.Equal(t, 1, frags[0].children[0].headingLevel)
		require.Equal(t, 1, frags[1].headingLevel)
	})

	t.Run("nested headings", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Chapter 1\nBody1\n## Section 1.1\nBody1.1\n## Section 1.2\nBody1.2\n"
		path := writeMDFile(t, dir, "nested.md", content)
		frags, err := (&Source{logging: false}).parseMDDocument(path)
		require.NoError(t, err)
		require.Len(t, frags, 1)
		require.Equal(t, "Chapter 1", frags[0].name)
		require.Len(t, frags[0].children, 2)
		require.Equal(t, "Section 1.1", frags[0].children[0].name)
		require.Equal(t, "Section 1.2", frags[0].children[1].name)
		require.Equal(t, 2, frags[0].children[0].headingLevel)
	})

	t.Run("headingPath accumulation", func(t *testing.T) {
		dir := t.TempDir()
		content := "# A\n## B\n### C\n"
		path := writeMDFile(t, dir, "headingpath.md", content)
		frags, err := (&Source{logging: false}).parseMDDocument(path)
		require.NoError(t, err)
		require.Len(t, frags, 1)
		chA := frags[0]
		require.Equal(t, "A", chA.headingPath)
		require.Len(t, chA.children, 1)
		chB := chA.children[0]
		require.Equal(t, "A > B", chB.headingPath)
		require.Len(t, chB.children, 1)
		chC := chB.children[0]
		require.Equal(t, "A > B > C", chC.headingPath)
	})

	t.Run("document with no headings creates default chapter", func(t *testing.T) {
		dir := t.TempDir()
		path := writeMDFile(t, dir, "empty.md", "第一段内容。\n\n第二段内容。\n")
		frags, err := (&Source{logging: false}).parseMDDocument(path)
		require.NoError(t, err)
		require.Len(t, frags, 1, "should create one default chapter")
		ch := frags[0]
		require.Equal(t, "empty", ch.name)
		require.NotEmpty(t, ch.headingPath)
		require.Len(t, ch.children, 2, "body should be split into fragments under the chapter")
		require.Equal(t, "empty", ch.children[0].headingPath)
	})

	t.Run("file not found", func(t *testing.T) {
		dir := t.TempDir()
		_, err := (&Source{}).parseMDDocument(filepath.Join(dir, "nonexistent.md"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "fragment:")
	})

	t.Run("unicode content", func(t *testing.T) {
		dir := t.TempDir()
		content := "# 导地线选型\n根据系统规划，导线型号JLHA1\n"
		path := writeMDFile(t, dir, "unicode.md", content)
		frags, err := (&Source{logging: false}).parseMDDocument(path)
		require.NoError(t, err)
		// 顶层无子标题 -> chapter，正文在其 body fragment 子节点。
		require.Len(t, frags, 1)
		require.Equal(t, "导地线选型", frags[0].name)
		require.Len(t, frags[0].children, 1)
		require.Contains(t, frags[0].children[0].content, "JLHA1")
	})

	t.Run("line numbers", func(t *testing.T) {
		dir := t.TempDir()
		content := "# First\nLine2\nLine3\n# Second\nLine5\n"
		path := writeMDFile(t, dir, "lines.md", content)
		frags, err := (&Source{logging: false}).parseMDDocument(path)
		require.NoError(t, err)
		require.Len(t, frags, 2)
		require.Equal(t, 1, frags[0].startLine)
		require.Equal(t, 4, frags[1].startLine)
	})
}

func TestFlattenFragments(t *testing.T) {
	t.Run("flat fragments produce NEXT chain", func(t *testing.T) {
		frags := []fragment{
			{id: "a", name: "A"},
			{id: "b", name: "B"},
			{id: "c", name: "C"},
		}
		leaves, chapters, relations := flattenFragments(frags, "doc1")
		require.Len(t, leaves, 3)
		require.Empty(t, chapters)
		var nextEdges []relation
		for _, r := range relations {
			if r.typ == "NEXT" {
				nextEdges = append(nextEdges, r)
			}
		}
		require.Len(t, nextEdges, 2)
		require.Equal(t, "a", nextEdges[0].fromID)
		require.Equal(t, "b", nextEdges[0].toID)
		require.Equal(t, "b", nextEdges[1].fromID)
		require.Equal(t, "c", nextEdges[1].toID)
	})

	t.Run("single leaf has no NEXT edge", func(t *testing.T) {
		frags := []fragment{{id: "only", name: "Only"}}
		leaves, _, relations := flattenFragments(frags, "doc1")
		require.Len(t, leaves, 1)
		var nextEdges []relation
		for _, r := range relations {
			if r.typ == "NEXT" {
				nextEdges = append(nextEdges, r)
			}
		}
		require.Empty(t, nextEdges)
	})

	t.Run("nested fragments produce CONTAINS edges", func(t *testing.T) {
		frags := []fragment{
			{
				id: "ch1", name: "Chapter",
				children: []fragment{
					{id: "leaf1", name: "Leaf1"},
					{id: "leaf2", name: "Leaf2"},
				},
			},
		}
		leaves, chapters, relations := flattenFragments(frags, "doc1")
		require.Len(t, leaves, 2)
		require.Len(t, chapters, 1)
		var containsEdges []relation
		var nextEdges []relation
		for _, r := range relations {
			if r.typ == "CONTAINS" {
				containsEdges = append(containsEdges, r)
			}
			if r.typ == "NEXT" {
				nextEdges = append(nextEdges, r)
			}
		}
		require.Len(t, containsEdges, 2)
		require.Len(t, nextEdges, 1)
		require.Equal(t, "leaf1", nextEdges[0].fromID)
		require.Equal(t, "leaf2", nextEdges[0].toID)
	})

	t.Run("sibling chapters have NEXT, fragments across chapters do not", func(t *testing.T) {
		frags := []fragment{
			{id: "ch1", name: "Ch1", children: []fragment{{id: "l1", name: "L1"}}},
			{id: "ch2", name: "Ch2", children: []fragment{{id: "l2", name: "L2"}}},
		}
		_, _, relations := flattenFragments(frags, "doc1")
		// 兄弟 chapter 之间应有 NEXT
		chapterNext := false
		for _, r := range relations {
			if r.typ == "NEXT" && r.fromID == "ch1" && r.toID == "ch2" {
				chapterNext = true
			}
		}
		require.True(t, chapterNext, "sibling chapters should have NEXT edge")
		// 不同 chapter 的 fragment 之间不应有 NEXT
		for _, r := range relations {
			require.False(t, r.typ == "NEXT" &&
				(r.fromID == "l1" || r.toID == "l1" || r.fromID == "l2" || r.toID == "l2"),
				"unexpected NEXT involving cross-chapter fragment: %s -> %s", r.fromID, r.toID)
		}
	})

	t.Run("fragmentIndex increments across leaves", func(t *testing.T) {
		frags := []fragment{
			{id: "a", name: "A"},
			{id: "b", name: "B"},
			{id: "c", name: "C"},
		}
		leaves, _, _ := flattenFragments(frags, "doc1")
		require.Equal(t, 1, leaves[0].fragmentIndex)
		require.Equal(t, 2, leaves[1].fragmentIndex)
		require.Equal(t, 3, leaves[2].fragmentIndex)
	})

	t.Run("fragmentIndex is per-chapter local, not global", func(t *testing.T) {
		// 两个 chapter 各含 2 个 leaf。修复前 fragmentIndex 是文档级全局自增
		// （ch2 的第一个 leaf 会得到 3）；修复后每个 chapter 独立从 1 开始计数。
		frags := []fragment{
			{
				id: "ch1", name: "Ch1",
				children: []fragment{
					{id: "l1", name: "L1"},
					{id: "l2", name: "L2"},
				},
			},
			{
				id: "ch2", name: "Ch2",
				children: []fragment{
					{id: "l3", name: "L3"},
					{id: "l4", name: "L4"},
				},
			},
		}
		leaves, _, _ := flattenFragments(frags, "doc1")
		byID := make(map[string]fragment)
		for _, l := range leaves {
			byID[l.id] = l
		}
		require.Equal(t, 1, byID["l1"].fragmentIndex)
		require.Equal(t, 2, byID["l2"].fragmentIndex)
		// 关键：ch2 下的 leaf 在各自父 chapter 内重新从 1 计数，而非全局 3/4。
		require.Equal(t, 1, byID["l3"].fragmentIndex)
		require.Equal(t, 2, byID["l4"].fragmentIndex)
		// 反过来确认：绝不能出现全局 3/4。
		require.NotEqual(t, 3, byID["l3"].fragmentIndex)
		require.NotEqual(t, 4, byID["l4"].fragmentIndex)
	})

	t.Run("documentID set on all fragments", func(t *testing.T) {
		frags := []fragment{
			{id: "ch1", name: "Ch1", children: []fragment{{id: "l1", name: "L1"}}},
		}
		_, chapters, _ := flattenFragments(frags, "mydoc")
		leaves, _, _ := flattenFragments(frags, "mydoc")
		require.Equal(t, "mydoc", chapters[0].documentID)
		require.Equal(t, "mydoc", leaves[0].documentID)
	})

	t.Run("deep nesting L1>L2>L3>leaf", func(t *testing.T) {
		frags := []fragment{
			{
				id: "l1", name: "L1",
				children: []fragment{
					{
						id: "l2", name: "L2",
						children: []fragment{
							{
								id: "l3", name: "L3",
								children: []fragment{
									{id: "leaf", name: "Leaf"},
								},
							},
						},
					},
				},
			},
		}
		leaves, chapters, relations := flattenFragments(frags, "doc1")
		require.Len(t, leaves, 1)
		require.Len(t, chapters, 3)
		containsCount := 0
		for _, r := range relations {
			if r.typ == "CONTAINS" {
				containsCount++
			}
		}
		require.Equal(t, 3, containsCount)
	})

	t.Run("no NEXT cross-chapter leak (order within parent preserved)", func(t *testing.T) {
		// 父 chapter P 下：子 chapter C(c1,c2) 与同级 leaf L
		frags := []fragment{
			{
				id: "P", name: "P",
				children: []fragment{
					{
						id: "C", name: "C",
						children: []fragment{
							{id: "c1", name: "c1"},
							{id: "c2", name: "c2"},
						},
					},
					{id: "L", name: "L"},
				},
			},
		}
		_, _, relations := flattenFragments(frags, "doc1")
		// 同一父 chapter C 内的 fragment 应有 NEXT
		inChapterNext := false
		for _, r := range relations {
			if r.typ == "NEXT" && r.fromID == "c1" && r.toID == "c2" {
				inChapterNext = true
			}
		}
		require.True(t, inChapterNext, "fragments within the same parent chapter should have NEXT")
		// 跨 chapter 的 fragment 之间不应有 NEXT
		for _, r := range relations {
			require.False(t, r.typ == "NEXT" &&
				((r.fromID == "c2" && r.toID == "L") || (r.fromID == "L" && r.toID == "c2")),
				"NEXT should not cross chapters: %s -> %s", r.fromID, r.toID)
		}
	})

	t.Run("top-level sibling chapters have NEXT", func(t *testing.T) {
		frags := []fragment{
			{
				id: "A", name: "A",
				children: []fragment{{id: "a1", name: "a1"}, {id: "a2", name: "a2"}},
			},
			{
				id: "B", name: "B",
				children: []fragment{{id: "b1", name: "b1"}, {id: "b2", name: "b2"}},
			},
		}
		_, _, relations := flattenFragments(frags, "doc1")
		chapterNext := false
		for _, r := range relations {
			if r.typ == "NEXT" && r.fromID == "A" && r.toID == "B" {
				chapterNext = true
			}
		}
		require.True(t, chapterNext, "top-level sibling chapters should have NEXT edge")
	})
}

func sortNodesByID(nodes []*graph.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})
}

func sortEdgesByKey(edges []*graph.Edge) {
	sort.Slice(edges, func(i, j int) bool {
		k_i := edges[i].FromID + "::" + edges[i].Type + "::" + edges[i].ToID
		k_j := edges[j].FromID + "::" + edges[j].Type + "::" + edges[j].ToID
		return k_i < k_j
	})
}

func nodeIDs(nodes []*graph.Node) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}

func hasNodeWithMetadata(nodes []*graph.Node, nodeType string, key string, value any) bool {
	for _, n := range nodes {
		if n.Metadata["trpc_ast_type"] == nodeType {
			if v, ok := n.Metadata[key]; ok && v == value {
				return true
			}
		}
	}
	return false
}

func newTestSource(skeletons []SkeletonNode, docPaths []string, opts ...Option) *Source {
	return New(skeletons, docPaths, append([]Option{WithLogging(false)}, opts...)...)
}

func TestReadGraph(t *testing.T) {

	t.Run("skeleton nodes with HAS_CHILD", func(t *testing.T) {
		skeletons := []SkeletonNode{
			{ID: "Root", Name: "Root"},
			{ID: "Child1", Name: "Child1", ParentID: "Root"},
			{ID: "Child2", Name: "Child2", ParentID: "Root"},
		}
		src := newTestSource(skeletons, nil)
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)
		require.NotNil(t, data)

		skeletonNodes := 0
		unknownFound := false
		for _, n := range data.Nodes {
			if n.Metadata["trpc_ast_type"] == "skeleton" {
				skeletonNodes++
			}
			if n.ID == defaultUnknownSkeletonID && n.Metadata["trpc_ast_auto"] == "unknown" {
				unknownFound = true
			}
		}
		require.Equal(t, 4, skeletonNodes)
		require.True(t, unknownFound, "auto unknown skeleton node should be present")

		hasChildEdges := 0
		for _, e := range data.Edges {
			if e.Type == "HAS_CHILD" {
				hasChildEdges++
				require.Equal(t, "Root", e.FromID)
			}
		}
		require.Equal(t, 2, hasChildEdges)
	})

	t.Run("MD document with chapters and fragments", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Chapter 1\nBody1\n## Section 1.1\nBody1.1\n## Section 1.2\nBody1.2\n"
		path := writeMDFile(t, dir, "doc1.md", content)
		skeletons := []SkeletonNode{
			{ID: "Skel1", Name: "Skel1"},
		}
		src := newTestSource(skeletons, []string{path})
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)
		require.NotNil(t, data)

		types := make(map[string]int)
		for _, n := range data.Nodes {
			if t, ok := n.Metadata["trpc_ast_type"].(string); ok {
				types[t]++
			}
		}
		require.Equal(t, 2, types["skeleton"])
		require.Equal(t, 1, types["document"])
		require.Equal(t, 1, types["chapter"])
		require.Equal(t, 2, types["fragment"])

		edgeTypes := make(map[string]int)
		for _, e := range data.Edges {
			edgeTypes[e.Type]++
		}
		require.GreaterOrEqual(t, edgeTypes["CONTAINS"], 2)
	})

	t.Run("5.4 top-level heading without sub-headings becomes a chapter", func(t *testing.T) {
		dir := t.TempDir()
		// 前言 / 附录 顶层无子标题；第一章 有子标题。
		content := "# 前言\n本文档是某设计规范。\n# 第一章 总则\n本章规定总体要求。\n## 1.1 适用范围\n适用于全省。\n## 1.2 术语定义\n术语如下。\n# 附录\n附录给出参考表格。\n"
		path := writeMDFile(t, dir, "ch54.md", content)
		src := newTestSource(nil, []string{path})
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)

		// 按类型收集节点：chapter 与其 body fragment 可能同名，故按类型区分。
		chaptersByName := map[string]*graph.Node{}
		fragmentsByName := map[string][]*graph.Node{}
		for _, n := range data.Nodes {
			switch n.Metadata[trpcAstMetaPrefix+"type"] {
			case "chapter":
				chaptersByName[n.Name] = n
			case "fragment":
				fragmentsByName[n.Name] = append(fragmentsByName[n.Name], n)
			}
		}
		childrenOf := map[string][]string{}
		for _, e := range data.Edges {
			if e.Type == "CONTAINS" {
				childrenOf[e.FromID] = append(childrenOf[e.FromID], e.ToID)
			}
		}

		// 前言 / 附录 现在都是 chapter 类型，而非 document 级 fragment。
		require.NotNil(t, chaptersByName["前言"], "前言 应为 chapter")
		require.NotNil(t, chaptersByName["附录"], "附录 应为 chapter")
		require.NotNil(t, chaptersByName["第一章 总则"], "第一章 应为 chapter")

		// 前言 作为一个 chapter，其正文挂在一个 fragment 子节点下。
		chID := chaptersByName["前言"].ID
		require.Len(t, childrenOf[chID], 1, "前言 chapter 应恰好有 1 个 fragment 子节点")
		childID := childrenOf[chID][0]
		var childNode *graph.Node
		for _, n := range data.Nodes {
			if n.ID == childID {
				childNode = n
			}
		}
		require.NotNil(t, childNode)
		require.Equal(t, "fragment", childNode.Metadata[trpcAstMetaPrefix+"type"])
		require.Contains(t, childNode.Content, "本文档是某设计规范。")

		// chapter 之间生成 NEXT（前言 -> 第一章 -> 附录），形成阅读顺序。
		nextPairs := map[string]string{}
		for _, e := range data.Edges {
			if e.Type == "NEXT" {
				nextPairs[e.FromID] = e.ToID
			}
		}
		require.Equal(t, chaptersByName["第一章 总则"].ID, nextPairs[chID])
		require.Equal(t, chaptersByName["附录"].ID, nextPairs[chaptersByName["第一章 总则"].ID])
	})

	t.Run("MOUNTS_TO from fragment with skeleton", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Ch1\nBody1\n## Sec1.1\nBody1.1\n"
		path := writeMDFile(t, dir, "mount.md", content)
		skeletons := []SkeletonNode{
			{ID: "TowerDesign", Name: "TowerDesign"},
		}
		src := newTestSource(skeletons, []string{path},
			WithName("mount-test"),
		)
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)
		require.NotNil(t, data)

		mountsCount := 0
		for _, e := range data.Edges {
			if e.Type == "MOUNTS_TO" {
				mountsCount++
				// title "Ch1/Sec1.1" matches no skeleton name -> auto unknown node
				require.Equal(t, defaultUnknownSkeletonID, e.ToID)
			}
		}
		require.GreaterOrEqual(t, mountsCount, 1, "should have MOUNTS_TO edges to skeleton")

		unknownFound := false
		for _, n := range data.Nodes {
			if n.ID == defaultUnknownSkeletonID {
				unknownFound = true
			}
		}
		require.True(t, unknownFound, "auto unknown skeleton node should be present")
	})

	t.Run("fragment without skeleton has no MOUNTS_TO", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Ch1\nBody1\n"
		path := writeMDFile(t, dir, "nomount.md", content)
		src := newTestSource(nil, []string{path})
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)
		for _, e := range data.Edges {
			require.NotEqual(t, "MOUNTS_TO", e.Type)
		}
	})

	t.Run("document node Content is empty", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Ch1\nBody1\n"
		path := writeMDFile(t, dir, "doccontent.md", content)
		src := newTestSource(nil, []string{path})
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)
		for _, n := range data.Nodes {
			if n.Metadata["trpc_ast_type"] == "document" {
				require.Empty(t, n.Content)
			}
		}
	})

	t.Run("NEXT chain between leaf fragments", func(t *testing.T) {
		dir := t.TempDir()
		content := "# A\n## A.1\nA.1 body\n## A.2\nA.2 body\n"
		path := writeMDFile(t, dir, "nextchain.md", content)
		src := newTestSource(nil, []string{path})
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)

		nextCount := 0
		for _, e := range data.Edges {
			if e.Type == "NEXT" {
				nextCount++
			}
		}
		require.GreaterOrEqual(t, nextCount, 1, "leaf fragments within a chapter should have NEXT edges")
	})

	t.Run("empty skeletons and docPaths", func(t *testing.T) {
		src := newTestSource(nil, nil)
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)
		require.Empty(t, data.Nodes)
		require.Empty(t, data.Edges)
	})

	t.Run("deterministic node IDs", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Ch1\nBody1\n"
		path := writeMDFile(t, dir, "det.md", content)
		src := newTestSource(nil, []string{path})
		data1, err := src.ReadGraph(context.Background())
		require.NoError(t, err)
		data2, err := src.ReadGraph(context.Background())
		require.NoError(t, err)

		sortNodesByID(data1.Nodes)
		sortNodesByID(data2.Nodes)
		require.Equal(t, nodeIDs(data1.Nodes), nodeIDs(data2.Nodes))
	})

	t.Run("node dedup via edgeMap", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Ch1\nBody1\n"
		path := writeMDFile(t, dir, "dedup.md", content)
		skeletons := []SkeletonNode{
			{ID: "Root", Name: "Root"},
			{ID: "Child", Name: "Child", ParentID: "Root"},
		}
		src := newTestSource(skeletons, []string{path})
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)

		edgeKeys := make(map[string]int)
		for _, e := range data.Edges {
			key := e.FromID + "::" + e.Type + "::" + e.ToID
			edgeKeys[key]++
		}
		for key, count := range edgeKeys {
			require.Equal(t, 1, count, "duplicate edge key: %s", key)
		}
	})

	t.Run("skeleton metadata preserved", func(t *testing.T) {
		skeletons := []SkeletonNode{
			{
				ID:       "Sk1",
				Name:     "Sk1",
				Content:  "Description",
				Metadata: map[string]any{"version": "7.0"},
			},
		}
		src := newTestSource(skeletons, nil)
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)
		for _, n := range data.Nodes {
			if n.ID == "Sk1" {
				require.Equal(t, "7.0", n.Metadata["version"])
				require.Equal(t, "skeleton", n.Metadata["trpc_ast_type"])
				require.Equal(t, "Description", n.Content)
			}
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		src := newTestSource(nil, nil)
		_, err := src.ReadGraph(ctx)
		require.Error(t, err)
	})

	t.Run("fragment metadata fields", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Ch1\nBody1\n## Sec1.1\nBody1.1\n"
		path := writeMDFile(t, dir, "meta.md", content)
		src := newTestSource(nil, []string{path})
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)

		for _, n := range data.Nodes {
			if n.Metadata["trpc_ast_type"] == "fragment" {
				require.NotEmpty(t, n.Metadata["trpc_ast_heading_path"])
				require.NotNil(t, n.Metadata["trpc_ast_heading_level"])
				require.NotNil(t, n.Metadata["trpc_ast_start_line"])
				require.NotNil(t, n.Metadata["trpc_ast_end_line"])
				require.NotNil(t, n.Metadata["trpc_ast_fragment_index"])
				require.Equal(t, "fragment", n.Metadata["trpc_ast_scope"])
				require.Equal(t, "text", n.Metadata["trpc_ast_fragment_type"])
			}
		}
	})

	t.Run("chapter metadata fields", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Ch1\nBody1\n## Sec1.1\nBody1.1\n"
		path := writeMDFile(t, dir, "chmeta.md", content)
		src := newTestSource(nil, []string{path})
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)

		for _, n := range data.Nodes {
			if n.Metadata["trpc_ast_type"] == "chapter" {
				require.NotEmpty(t, n.Metadata["trpc_ast_heading_path"])
				require.NotNil(t, n.Metadata["trpc_ast_heading_level"])
				require.NotEmpty(t, n.Metadata["trpc_ast_document_id"])
				require.Equal(t, "chapter", n.Metadata["trpc_ast_scope"])
			}
		}
	})

	t.Run("CONTAINS from document to top-level chapters", func(t *testing.T) {
		dir := t.TempDir()
		content := "# Ch1\nBody1\n# Ch2\nBody2\n"
		path := writeMDFile(t, dir, "doc2ch.md", content)
		src := newTestSource(nil, []string{path})
		data, err := src.ReadGraph(context.Background())
		require.NoError(t, err)

		docContains := 0
		for _, e := range data.Edges {
			if e.Type == "CONTAINS" && strings.HasPrefix(e.FromID, "doc:") {
				docContains++
			}
		}
		require.Equal(t, 2, docContains, "document should have CONTAINS edges to top-level chapters")
	})

	t.Run("file not found error", func(t *testing.T) {
		dir := t.TempDir()
		src := newTestSource(nil, []string{filepath.Join(dir, "nonexistent.md")})
		_, err := src.ReadGraph(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "fragment:")
	})
}

func TestReadGraphIntegrationWithSkeleton(t *testing.T) {
	dir := t.TempDir()
	content := "# Chapter 1\nBody1\n## Section 1.1\nBody1.1\n## Section 1.2\nBody1.2\n# Chapter 2\nBody2\n## Section 2.1\nBody2.1\n## Section 2.2\nBody2.2\n"
	path := writeMDFile(t, dir, "full.md", content)

	skeletons := []SkeletonNode{
		{ID: "EngineeringProject", Name: "EngineeringProject", Content: "Root"},
		{ID: "TowerDesign", Name: "TowerDesign", ParentID: "EngineeringProject"},
		{ID: "ConductorAndGroundWire", Name: "ConductorAndGroundWire", ParentID: "EngineeringProject"},
	}

	src := newTestSource(skeletons, []string{path}, WithName("integration-test"))
	data, err := src.ReadGraph(context.Background())
	require.NoError(t, err)
	require.NotNil(t, data)

	types := make(map[string]int)
	for _, n := range data.Nodes {
		if t, ok := n.Metadata["trpc_ast_type"].(string); ok {
			types[t]++
		}
	}
	require.Equal(t, 4, types["skeleton"])
	require.Equal(t, 1, types["document"])
	require.Equal(t, 2, types["chapter"])
	require.Equal(t, 4, types["fragment"])

	unknownFound := false
	for _, n := range data.Nodes {
		if n.ID == defaultUnknownSkeletonID {
			unknownFound = true
		}
	}
	require.True(t, unknownFound, "auto unknown skeleton node should be present")

	edgeTypes := make(map[string]int)
	for _, e := range data.Edges {
		edgeTypes[e.Type]++
	}
	require.Equal(t, 2, edgeTypes["HAS_CHILD"])
	require.GreaterOrEqual(t, edgeTypes["CONTAINS"], 2)
	require.GreaterOrEqual(t, edgeTypes["NEXT"], 2)
}

// TestAssignSkeleton verifies that fragments are mounted to the most specific
// skeleton node whose heading path matches, instead of always to the first root.
func TestAssignSkeleton(t *testing.T) {
	t.Run("empty skeletons returns empty", func(t *testing.T) {
		src := newTestSource(nil, nil)
		require.Equal(t, "", src.assignSkeleton(fragment{headingPath: "A > B"}))
	})

	t.Run("deepest path match", func(t *testing.T) {
		skeletons := []SkeletonNode{
			{ID: "s1", Name: "1 总则"},
			{ID: "s11", Name: "1.1 设计依据", ParentID: "s1"},
			{ID: "s111", Name: "1.1.1 范围", ParentID: "s11"},
		}
		src := newTestSource(skeletons, nil)
		got := src.assignSkeleton(fragment{headingPath: "1 总则 > 1.1 设计依据 > 1.1.1 范围"})
		require.Equal(t, "s111", got)
	})

	t.Run("mounts to deepest defined when fragment is deeper than skeleton", func(t *testing.T) {
		skeletons := []SkeletonNode{
			{ID: "s1", Name: "1 总则"},
			{ID: "s11", Name: "1.1 设计依据", ParentID: "s1"},
		}
		src := newTestSource(skeletons, nil)
		// fragment path goes one level deeper than the skeleton tree defines
		got := src.assignSkeleton(fragment{headingPath: "1 总则 > 1.1 设计依据 > 1.1.1 范围"})
		require.Equal(t, "s11", got)
	})

	t.Run("falls back to unknown node when top level mismatches", func(t *testing.T) {
		skeletons := []SkeletonNode{
			{ID: "root", Name: "EngineeringProject"},
			{ID: "c1", Name: "TowerDesign", ParentID: "root"},
		}
		src := newTestSource(skeletons, nil)
		got := src.assignSkeleton(fragment{headingPath: "Chapter 1 > Section 1.1"})
		require.Equal(t, defaultUnknownSkeletonID, got)
	})

	t.Run("normalizes numbering styles", func(t *testing.T) {
		skeletons := []SkeletonNode{
			{ID: "s1", Name: "1 总则"},
			{ID: "s11", Name: "1.1 设计依据", ParentID: "s1"},
		}
		src := newTestSource(skeletons, nil)
		// document uses a Chinese numbering prefix, skeleton uses digits
		got := src.assignSkeleton(fragment{headingPath: "第一章 总则 > 1.1 设计依据"})
		require.Equal(t, "s11", got)
	})

	t.Run("multiple roots match independently", func(t *testing.T) {
		skeletons := []SkeletonNode{
			{ID: "rA", Name: "A 总述"},
			{ID: "rB", Name: "B 总述"},
			{ID: "rA1", Name: "A.1 细节", ParentID: "rA"},
			{ID: "rB1", Name: "B.1 细节", ParentID: "rB"},
		}
		src := newTestSource(skeletons, nil)
		gotA := src.assignSkeleton(fragment{headingPath: "A 总述 > A.1 细节"})
		require.Equal(t, "rA1", gotA)
		gotB := src.assignSkeleton(fragment{headingPath: "B 总述 > B.1 细节"})
		require.Equal(t, "rB1", gotB)
	})

	t.Run("falls back to unknown node when skeletons have no roots", func(t *testing.T) {
		skeletons := []SkeletonNode{
			{ID: "orphan", Name: "Orphan", ParentID: "missing"},
		}
		src := newTestSource(skeletons, nil)
		got := src.assignSkeleton(fragment{headingPath: "X > Y"})
		require.Equal(t, defaultUnknownSkeletonID, got)
	})
}

// TestReadGraphMountsToMatchedSkeleton is an end-to-end check: with a skeleton
// tree that mirrors the document headings, each fragment's MOUNTS_TO edge must
// point at the matching skeleton node (not all at the first root).
func TestReadGraphMountsToMatchedSkeleton(t *testing.T) {
	dir := t.TempDir()
	content := "# 1 总则\nB0\n## 1.1 设计依据\nB1\n### 1.1.1 范围\nB2\n## 1.2 术语\nB3\n"
	path := writeMDFile(t, dir, "mount2.md", content)
	skeletons := []SkeletonNode{
		{ID: "s1", Name: "1 总则"},
		{ID: "s11", Name: "1.1 设计依据", ParentID: "s1"},
		{ID: "s111", Name: "1.1.1 范围", ParentID: "s11"},
		{ID: "s12", Name: "1.2 术语", ParentID: "s1"},
	}
	src := newTestSource(skeletons, []string{path})
	data, err := src.ReadGraph(context.Background())
	require.NoError(t, err)
	require.NotNil(t, data)

	fragPathByNode := map[string]string{}
	for _, n := range data.Nodes {
		if n.Metadata["trpc_ast_type"] == "fragment" {
			if p, ok := n.Metadata["trpc_ast_heading_path"].(string); ok {
				fragPathByNode[n.ID] = p
			}
		}
	}

	mountTarget := map[string]string{}
	for _, e := range data.Edges {
		if e.Type == "MOUNTS_TO" {
			if p, ok := fragPathByNode[e.FromID]; ok {
				mountTarget[p] = e.ToID
			}
		}
	}

	require.Equal(t, "s111", mountTarget["1 总则 > 1.1 设计依据 > 1.1.1 范围"],
		"deepest fragment should mount to the deepest skeleton node")
	require.Equal(t, "s12", mountTarget["1 总则 > 1.2 术语"],
		"shallower fragment should mount to its own skeleton node, not the root")
	// Ensure fragments are NOT all mounted to the same root node.
	roots := make(map[string]bool)
	for _, id := range mountTarget {
		roots[id] = true
	}
	require.Greater(t, len(roots), 1, "fragments must mount to distinct skeleton nodes")
}

// TestReadGraphUnmatchedToUnknown verifies that fragments whose heading
// path matches no skeleton node are all mounted to the auto "unknown data"
// node, which itself appears in the graph as a skeleton node.
func TestReadGraphUnmatchedToUnknown(t *testing.T) {
	dir := t.TempDir()
	// Document headings share nothing with the skeleton node names.
	content := "# Chapter 1\nB1\n## Section A\nB2\n"
	path := writeMDFile(t, dir, "unmatched.md", content)
	skeletons := []SkeletonNode{
		{ID: "Root", Name: "Root"},
		{ID: "Child", Name: "Child", ParentID: "Root"},
	}
	src := newTestSource(skeletons, []string{path})
	data, err := src.ReadGraph(context.Background())
	require.NoError(t, err)
	require.NotNil(t, data)

	fragPath := map[string]string{}
	for _, n := range data.Nodes {
		if n.Metadata["trpc_ast_type"] == "fragment" {
			if p, ok := n.Metadata["trpc_ast_heading_path"].(string); ok {
				fragPath[n.ID] = p
			}
		}
	}
	mountTarget := map[string]string{}
	for _, e := range data.Edges {
		if e.Type == "MOUNTS_TO" {
			if p, ok := fragPath[e.FromID]; ok {
				mountTarget[p] = e.ToID
			}
		}
	}
	require.NotEmpty(t, mountTarget, "should have at least one MOUNTS_TO edge")
	for p, target := range mountTarget {
		require.Equal(t, defaultUnknownSkeletonID, target,
			"unmatched fragment %q must mount to the unknown node", p)
	}

	unknown := false
	for _, n := range data.Nodes {
		if n.ID == defaultUnknownSkeletonID {
			unknown = true
			require.Equal(t, "未知数据", n.Name)
		}
	}
	require.True(t, unknown, "auto unknown skeleton node must exist in the graph")
}
