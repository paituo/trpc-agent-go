//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package fragment provides a graph source that parses Markdown documents into
// a four-layer graph structure: skeleton → document → chapter → fragment.
package fragment

import (
	"context"
	//nolint:gosec // Used only for stable graph IDs, not cryptographic security.
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

const defaultSourceName = "Fragment Source"

// trpcAstMetaPrefix is the prefix for all metadata keys written by fragment source,
// consistent with codeast.TrpcAstMetaPrefix used by repo source.
const trpcAstMetaPrefix = "trpc_ast_"

// SkeletonNode describes a skeleton node from an engineering skeleton definition.
type SkeletonNode struct {
	ID       string
	Name     string
	Content  string
	ParentID string
	Metadata map[string]any
}

type fragment struct {
	id            string
	name          string
	content       string
	headingLevel  int
	headingPath   string
	startLine     int
	endLine       int
	children      []fragment
	documentID    string
	fragmentIndex int
}

type relation struct {
	fromID string
	toID   string
	typ    string
}

// Source implements source.GraphSource for fragment-based graph data.
type Source struct {
	name      string
	skeletons []SkeletonNode
	docPaths  []string
	metadata  map[string]any
	logging   bool

	// skeleton index (lazily built) for matching fragments to skeleton nodes.
	skeletonRoots []*skeletonTreeNode
	skeletonBuilt bool
	// skeletonView caches the effective skeleton list (user skeletons plus the
	// auto "unknown data" node) used for both indexing and graph emission.
	skeletonView []SkeletonNode

	// embedder / reranker enable semantic skeleton mounting. When both
	// are set, a semanticMounter is built in New and assignSkeleton
	// delegates to it; otherwise the deterministic heading-path matcher is used.
	embedder embedder.Embedder
	reranker reranker.Reranker
	mounter  *semanticMounter
}

// skeletonTreeNode is a node in the in-memory skeleton tree built from
// Source.skeletons (keyed by ParentID).
type skeletonTreeNode struct {
	node     SkeletonNode
	children []*skeletonTreeNode
}

// New creates a FragmentGraphSource with the given skeleton nodes and document paths.
func New(skeletons []SkeletonNode, docPaths []string, opts ...Option) *Source {
	s := &Source{
		name:      defaultSourceName,
		skeletons: skeletons,
		docPaths:  docPaths,
		metadata:  make(map[string]any),
		logging:   true,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.embedder != nil && s.reranker != nil {
		s.mounter = newSemanticMounter(s.embedder, s.reranker, defaultUnknownSkeletonID, skeletons)
	}
	return s
}

func (s *Source) logf(format string, args ...any) {
	if s.logging {
		log.Printf("[fragment] "+format, args...)
	}
}

// ReadGraph reads all documents and skeleton definitions, returning a graph.Data
// containing skeleton, document, chapter, and fragment nodes with their edges.
func (s *Source) ReadGraph(ctx context.Context, opts ...source.ReadGraphOption) (*graph.Data, error) {
	s.logf("ReadGraph start: skeletons=%d docPaths=%d", len(s.skeletons), len(s.docPaths))
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	nodeMap := make(map[string]*graph.Node)
	edgeMap := make(map[string]*graph.Edge)

	var allLeaves []fragment

	// 先解析骨架文件：构建 skeleton 树（含自动的“未知数据”节点），
	// 以便后续文档 fragment 在解析时就能挂载到正确的骨架节点。
	s.ensureSkeletonIndex()
	for _, sk := range s.effectiveSkeletons() {
		if sk.ID == "" {
			s.logf("Skipping skeleton node with empty ID, name=%q", sk.Name)
			continue
		}
		metadata := cloneMap(sk.Metadata)
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata[trpcAstMetaPrefix+"type"] = "skeleton"
		metadata[trpcAstMetaPrefix+"scope"] = "skeleton"

		nodeMap[sk.ID] = &graph.Node{
			ID:       sk.ID,
			Name:     sk.Name,
			Content:  sk.Content,
			Metadata: metadata,
		}
		if sk.ParentID != "" {
			edgeKey := sk.ParentID + "::HAS_CHILD::" + sk.ID
			edgeMap[edgeKey] = &graph.Edge{
				ID:     edgeKey,
				FromID: sk.ParentID,
				ToID:   sk.ID,
				Type:   "HAS_CHILD",
				Metadata: map[string]any{
					"builder": "fragment_graph_source",
				},
			}
		}
	}

	for _, docPath := range s.docPaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		frags, err := s.parseMDDocument(docPath)
		if err != nil {
			return nil, err
		}
		docID := "doc:" + docPath
		leaves, chapters, relations := flattenFragments(frags, docID)
		allLeaves = append(allLeaves, leaves...)
		s.logf("Parsed doc %s: headings→tree ok, leaves=%d chapters=%d relations=%d",
			filepath.Base(docPath), len(leaves), len(chapters), len(relations))

		nodeMap[docID] = &graph.Node{
			ID:      docID,
			Name:    docPath,
			Content: "",
			Metadata: map[string]any{
				trpcAstMetaPrefix + "type":      "document",
				trpcAstMetaPrefix + "scope":     "document",
				trpcAstMetaPrefix + "file_path": docPath,
			},
		}

		for _, ch := range chapters {
			chNodeID := generateNodeID("fragment:" + ch.id)
			chMetadata := map[string]any{
				trpcAstMetaPrefix + "type":          "chapter",
				trpcAstMetaPrefix + "scope":         "chapter",
				trpcAstMetaPrefix + "heading_level": ch.headingLevel,
				trpcAstMetaPrefix + "heading_path":  ch.headingPath,
				trpcAstMetaPrefix + "document_id":   docID,
			}
			nodeMap[chNodeID] = &graph.Node{
				ID:       chNodeID,
				Name:     ch.name,
				Content:  ch.content,
				Metadata: chMetadata,
			}
		}

		for _, rel := range relations {
			if rel.typ == "CONTAINS_DOC" {
				edgeKey := docID + "::CONTAINS::" + generateNodeID("fragment:"+rel.toID)
				edgeMap[edgeKey] = &graph.Edge{
					ID:     edgeKey,
					FromID: docID,
					ToID:   generateNodeID("fragment:" + rel.toID),
					Type:   "CONTAINS",
					Metadata: map[string]any{
						"builder": "fragment_graph_source",
					},
				}
			} else {
				fromID := generateNodeID("fragment:" + rel.fromID)
				toID := generateNodeID("fragment:" + rel.toID)
				edgeKey := fromID + "::" + rel.typ + "::" + toID
				edgeMap[edgeKey] = &graph.Edge{
					ID:     edgeKey,
					FromID: fromID,
					ToID:   toID,
					Type:   rel.typ,
					Metadata: map[string]any{
						"builder": "fragment_graph_source",
					},
				}
			}
		}
	}

	for _, leaf := range allLeaves {
		if leaf.id == "" {
			continue
		}
		nodeID := generateNodeID("fragment:" + leaf.id)
		metadata := map[string]any{
			trpcAstMetaPrefix + "type":           "fragment",
			trpcAstMetaPrefix + "scope":          "fragment",
			trpcAstMetaPrefix + "fragment_id":    leaf.id,
			trpcAstMetaPrefix + "fragment_type":  "text",
			trpcAstMetaPrefix + "document_id":    leaf.documentID,
			trpcAstMetaPrefix + "heading_path":   leaf.headingPath,
			trpcAstMetaPrefix + "heading_level":  leaf.headingLevel,
			trpcAstMetaPrefix + "start_line":     leaf.startLine,
			trpcAstMetaPrefix + "end_line":       leaf.endLine,
			trpcAstMetaPrefix + "fragment_index": leaf.fragmentIndex,
		}

		// 语义挂接：配置了 embedder+reranker 时用向量召回+重排，
		// 否则退回确定性标题路径匹配。多节点会返回多条挂接。
		targets := s.mountFragment(ctx, leaf)
		if len(targets) > 0 {
			metadata[trpcAstMetaPrefix+"skeleton_node"] = targets[0].SkeletonID
			metadata[trpcAstMetaPrefix+"mount_type"] = targets[0].MountType
			metadata[trpcAstMetaPrefix+"match_source"] = targets[0].MatchSource
		}
		nodeMap[nodeID] = &graph.Node{
			ID:       nodeID,
			Name:     leaf.name,
			Content:  leaf.content,
			Metadata: metadata,
		}

		for _, t := range targets {
			edgeKey := nodeID + "::MOUNTS_TO::" + t.SkeletonID
			edgeMap[edgeKey] = &graph.Edge{
				ID:     edgeKey,
				FromID: nodeID,
				ToID:   t.SkeletonID,
				Type:   "MOUNTS_TO",
				Metadata: map[string]any{
					"builder":      "fragment_graph_source",
					"mount_type":   t.MountType,
					"match_source": t.MatchSource,
				},
			}
		}
	}

	nodes := make([]*graph.Node, 0, len(nodeMap))
	for _, node := range nodeMap {
		nodes = append(nodes, node)
	}
	edges := make([]*graph.Edge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		edges = append(edges, edge)
	}

	typeCounts := make(map[string]int)
	for _, n := range nodes {
		if t, ok := n.Metadata[trpcAstMetaPrefix+"type"].(string); ok {
			typeCounts[t]++
		}
	}
	edgeTypeCounts := make(map[string]int)
	for _, e := range edges {
		edgeTypeCounts[e.Type]++
	}
	s.logf("ReadGraph done: nodes=%d edges=%d nodeTypes=%v edgeTypes=%v",
		len(nodes), len(edges), typeCounts, edgeTypeCounts)

	return &graph.Data{Nodes: nodes, Edges: edges}, nil
}

func (s *Source) parseMDDocument(docPath string) ([]fragment, error) {
	data, err := os.ReadFile(docPath)
	if err != nil {
		return nil, fmt.Errorf("fragment: read file %s: %w", docPath, err)
	}
	content := string(data)
	lines := strings.Split(content, "\n")
	s.logf("parseMDDocument %s: %d lines", filepath.Base(docPath), len(lines))

	type heading struct {
		lineNum int
		level   int
		title   string
	}
	var headings []heading
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		level := 0
		for _, ch := range trimmed {
			if ch == '#' {
				level++
			} else {
				break
			}
		}
		if level <= 0 || level > 6 {
			continue
		}
		title := strings.TrimSpace(trimmed[level:])
		if title == "" {
			continue
		}
		headings = append(headings, heading{
			lineNum: i + 1,
			level:   level,
			title:   title,
		})
	}

	if len(headings) == 0 {
		// 文档没有任何标题：创建一个默认 chapter，并把正文分段挂到该 chapter 下，
		// 而不是挂在 document 下。
		return s.buildDefaultChapter(docPath, content, lines), nil
	}
	s.logf("parseMDDocument %s: found %d headings, building tree", filepath.Base(docPath), len(headings))

	var buildTree func(start, end int, parentLevel int, parentPath string) []fragment
	buildTree = func(start, end int, parentLevel int, parentPath string) []fragment {
		var result []fragment
		i := start
		for i < end {
			h := headings[i]
			if h.level <= parentLevel && i > start {
				break
			}
			j := i + 1
			for j < end && headings[j].level > h.level {
				j++
			}

			headingPath := h.title
			if parentPath != "" {
				headingPath = parentPath + " > " + h.title
			}

			children := buildTree(i+1, j, h.level, headingPath)

			contentStart := h.lineNum
			contentEnd := len(lines)
			if i+1 < len(headings) {
				contentEnd = headings[i+1].lineNum - 1
			}
			var bodyLines []string
			for k := contentStart; k <= contentEnd && k-1 < len(lines); k++ {
				if k-1 >= 0 && k-1 < len(lines) {
					bodyLines = append(bodyLines, lines[k-1])
				}
			}
			bodyText := strings.TrimSpace(strings.Join(bodyLines, "\n"))

			// 5.4 修复（口径 B）：顶层、且自身没有子标题的标题，
			// 建模为 chapter，并将其正文作为一个 fragment 挂到该 chapter 下，
			// 避免它沦为 document 级 fragment（与真正的 chapter 类型不一致）。
			if parentLevel == 0 && len(children) == 0 {
				chapterID := fmt.Sprintf("h_%d_%d", h.lineNum, h.level)
				bodyFragment := fragment{
					id:           chapterID + "_body",
					name:         h.title,
					content:      bodyText,
					headingLevel: h.level,
					headingPath:  headingPath,
					startLine:    contentStart,
					endLine:      contentEnd,
				}
				result = append(result, fragment{
					id:           chapterID,
					name:         h.title,
					content:      strings.TrimSpace(lines[h.lineNum-1]), // 仅标题行，作为章节头
					headingLevel: h.level,
					headingPath:  headingPath,
					startLine:    contentStart,
					endLine:      contentEnd,
					children:     []fragment{bodyFragment},
				})
				i = j
				continue
			}

			result = append(result, fragment{
				id:           fmt.Sprintf("h_%d_%d", h.lineNum, h.level),
				name:         h.title,
				content:      bodyText,
				headingLevel: h.level,
				headingPath:  headingPath,
				startLine:    contentStart,
				endLine:      contentEnd,
				children:     children,
			})
			i = j
		}
		return result
	}

	result := buildTree(0, len(headings), 0, "")
	s.logf("parseMDDocument %s: tree built, %d top-level fragments", filepath.Base(docPath), len(result))
	return result, nil
}

func flattenFragments(frags []fragment, docID string) ([]fragment, []fragment, []relation) {
	var leaves []fragment
	var chapters []fragment
	var relations []relation

	// walk 在每一层独立维护 prevLeafID / prevChapterID 与 index：
	//   - index 为“当前父节点（document 或 chapter）下”的局部自增序号，
	//     每个父节点独立从 1 开始计数，使 fragmentIndex 表示“在父 chapter 下的顺序”；
	//   - 递归进入子 chapter 时，不把子树末端 leaf 回写到本层 prevLeafID，
	//     从而避免不同 chapter 的 fragment 之间产生跨章 NEXT 边；
	//   - 仅在同一父 chapter 下的直接 leaf 兄弟之间生成 NEXT（保持原文顺序）；
	//   - 兄弟 chapter 之间生成 NEXT。
	var walk func(parents []fragment, frags []fragment)
	walk = func(parents []fragment, frags []fragment) {
		var prevLeafID string
		var prevChapterID string
		index := 0 // 每个父节点独立计数：fragmentIndex 表示该 leaf 在父节点下的局部序号
		for i := range frags {
			frags[i].documentID = docID
			hasChildren := len(frags[i].children) > 0

			// 与父节点（document 或 chapter）的 CONTAINS 关系
			if len(parents) == 0 {
				relations = append(relations, relation{
					fromID: "",
					toID:   frags[i].id,
					typ:    "CONTAINS_DOC",
				})
			} else {
				relations = append(relations, relation{
					fromID: parents[len(parents)-1].id,
					toID:   frags[i].id,
					typ:    "CONTAINS",
				})
			}

			if hasChildren {
				chapters = append(chapters, frags[i])
				// 兄弟 chapter 之间建立 NEXT 关系
				if prevChapterID != "" {
					relations = append(relations, relation{
						fromID: prevChapterID,
						toID:   frags[i].id,
						typ:    "NEXT",
					})
				}
				prevChapterID = frags[i].id
				// 递归处理子节点；不把子树末端 leaf 回写到本层 prevLeafID
				parentsCopy := make([]fragment, len(parents), len(parents)+1)
				copy(parentsCopy, parents)
				walk(append(parentsCopy, frags[i]), frags[i].children)
			} else {
				index++
				frags[i].fragmentIndex = index
				leaves = append(leaves, frags[i])
				// 仅同一父 chapter 下的直接 leaf 兄弟之间建立 NEXT，保持原文顺序
				if prevLeafID != "" {
					relations = append(relations, relation{
						fromID: prevLeafID,
						toID:   frags[i].id,
						typ:    "NEXT",
					})
				}
				prevLeafID = frags[i].id
			}
		}
	}
	walk(nil, frags)
	return leaves, chapters, relations
}

// textBlock 是正文切分后的一个段落块。
type textBlock struct {
	text      string
	startLine int
	endLine   int
}

// buildDefaultChapter 处理“文档无标题”的情况：创建一个默认 chapter，
// 并把正文按段落切分为 fragment 挂到该 chapter 之下，而不是挂在 document 之下。
func (s *Source) buildDefaultChapter(docPath, content string, lines []string) []fragment {
	chapterName := defaultChapterName(docPath)
	chapterID := docScopedID(docPath, "chapter")
	chapterPath := chapterName

	blocks := splitIntoBlocks(content)
	if len(blocks) == 0 {
		s.logf("parseMDDocument %s: no headings and empty body, skipping", filepath.Base(docPath))
		return nil
	}

	children := make([]fragment, 0, len(blocks))
	for _, b := range blocks {
		children = append(children, fragment{
			id:           docScopedID(docPath, fmt.Sprintf("block_%d", b.startLine)),
			name:         firstLineSnippet(b.text, 40),
			content:      strings.TrimSpace(b.text),
			headingLevel: 2,
			headingPath:  chapterPath,
			startLine:    b.startLine,
			endLine:      b.endLine,
		})
	}

	chapter := fragment{
		id:           chapterID,
		name:         chapterName,
		content:      strings.TrimSpace(content),
		headingLevel: 1,
		headingPath:  chapterPath,
		startLine:    1,
		endLine:      len(lines),
		children:     children,
	}
	s.logf("parseMDDocument %s: no headings, created default chapter %q with %d fragments",
		filepath.Base(docPath), chapterName, len(children))
	return []fragment{chapter}
}

// splitIntoBlocks 按空行把正文切分为段落块。
func splitIntoBlocks(content string) []textBlock {
	lines := strings.Split(content, "\n")
	var blocks []textBlock
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			if start >= 0 {
				blocks = append(blocks, textBlock{
					text:      strings.Join(lines[start:i], "\n"),
					startLine: start + 1,
					endLine:   i,
				})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		blocks = append(blocks, textBlock{
			text:      strings.Join(lines[start:], "\n"),
			startLine: start + 1,
			endLine:   len(lines),
		})
	}
	return blocks
}

// firstLineSnippet 取文本首个非空行作为片段名，超长截断。
func firstLineSnippet(text string, limit int) string {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			runes := []rune(t)
			if len(runes) > limit {
				return string(runes[:limit]) + "…"
			}
			return t
		}
	}
	return "片段"
}

// defaultChapterName 用文档基名（去扩展名）作为默认 chapter 名。
func defaultChapterName(docPath string) string {
	base := filepath.Base(docPath)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		return "正文"
	}
	return base
}

// docScopedID 生成与文档路径绑定的稳定 ID，避免不同文档之间节点 ID 冲突。
func docScopedID(docPath, local string) string {
	sum := sha1.Sum([]byte(docPath + "|" + local))
	return hex.EncodeToString(sum[:8])
}

func generateNodeID(key string) string {
	sum := sha1.Sum([]byte(key))
	return "node:" + hex.EncodeToString(sum[:12])
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cloned := make(map[string]any, len(m))
	for k, v := range m {
		cloned[k] = v
	}
	return cloned
}

// assignSkeleton maps a fragment to the most specific skeleton node whose
// heading path matches the fragment's heading path. It walks the skeleton tree
// level by level against the fragment's heading path components (joined by " > "),
// advancing as long as the normalized component matches a child of the current
// skeleton node. If no root-level component matches, it falls back to the first
// skeleton root so the fragment still mounts somewhere (conservative default).
//
// This replaces the previous behaviour that ignored the fragment entirely and
// always mounted every fragment onto the first skeleton root, which made the
// skeleton layer useless for hierarchical GraphRAG retrieval.
// defaultUnknownSkeletonID is the auto-generated skeleton node that collects
// every fragment whose heading path matches no real skeleton node.
const defaultUnknownSkeletonID = "skeleton:unknown"

// assignSkeleton maps a fragment to the most specific skeleton node whose
// heading path matches the fragment's heading path. If the fragment matches
// no skeleton node at all, it is mounted to the auto "unknown data" node so
// that unmatched content is still grouped under a single, queryable node.
func (s *Source) assignSkeleton(f fragment) string {
	if len(s.skeletons) == 0 {
		return ""
	}
	s.ensureSkeletonIndex()
	return s.matchSkeletonNode(f.headingPath)
}

// effectiveSkeletons returns the skeleton list used for indexing and graph
// emission: the user-provided skeletons plus an auto "unknown data" node
// (added only when at least one skeleton is provided). The result is cached
// in s.skeletonView.
func (s *Source) effectiveSkeletons() []SkeletonNode {
	if s.skeletonView != nil {
		return s.skeletonView
	}
	if len(s.skeletons) == 0 {
		return nil
	}
	// Reuse the user's node if they already defined an unknown node.
	for _, sk := range s.skeletons {
		if sk.ID == defaultUnknownSkeletonID {
			s.skeletonView = s.skeletons
			return s.skeletonView
		}
	}
	view := make([]SkeletonNode, 0, len(s.skeletons)+1)
	view = append(view, s.skeletons...)
	view = append(view, SkeletonNode{
		ID:       defaultUnknownSkeletonID,
		Name:     "未知数据",
		Content:  "未匹配到骨架定义的文档内容",
		ParentID: "",
		Metadata: map[string]any{trpcAstMetaPrefix + "auto": "unknown"},
	})
	s.skeletonView = view
	return view
}

// ensureSkeletonIndex builds the in-memory skeleton tree once, keyed by
// ParentID, from the effective skeleton list (which includes the auto
// "unknown data" node).
func (s *Source) ensureSkeletonIndex() {
	if s.skeletonBuilt {
		return
	}
	s.skeletonBuilt = true

	skeletons := s.effectiveSkeletons()
	byParent := make(map[string][]*skeletonTreeNode)
	all := make([]*skeletonTreeNode, 0, len(skeletons))
	for i := range skeletons {
		n := &skeletonTreeNode{node: skeletons[i]}
		byParent[skeletons[i].ParentID] = append(byParent[skeletons[i].ParentID], n)
		all = append(all, n)
	}
	for _, n := range all {
		n.children = byParent[n.node.ID]
	}
	s.skeletonRoots = byParent[""]
}

// matchSkeletonNode walks the skeleton tree against the fragment's heading
// path and returns the deepest skeleton node ID that matches. When nothing
// matches (including the top level), it returns the auto "unknown data" node
// ID so unmatched content is collected there.
func (s *Source) matchSkeletonNode(path string) string {
	components := strings.Split(path, " > ")
	current := s.skeletonRoots
	matched := ""
	for _, comp := range components {
		next := matchChildByKey(current, skeletonNormKey(comp))
		if next == nil {
			break
		}
		matched = next.node.ID
		current = next.children
	}
	if matched == "" {
		return defaultUnknownSkeletonID
	}
	return matched
}

// matchChildByKey returns the child whose skeletonNormKey matches key, or nil.
func matchChildByKey(children []*skeletonTreeNode, key string) *skeletonTreeNode {
	if key == "" {
		return nil
	}
	for _, c := range children {
		if skeletonNormKey(c.node.Name) == key {
			return c
		}
	}
	return nil
}

// skeletonNormKey normalizes a heading/skeleton name so that fragments and
// skeleton nodes written with different numbering styles still match. It strips
// leading numbering (both "1.1 " and "第一章 "/ "第1条 " forms) and collapses
// whitespace. For example "1.1 设计依据" and "设计依据" both normalize to
// "设计依据", and "第一章 总则" matches "1 总则".
func skeletonNormKey(name string) string {
	s := strings.TrimSpace(name)
	s = reLeadCNAndNumber.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	s = reCollapseSpace.ReplaceAllString(s, " ")
	return strings.ToLower(s)
}

var (
	// Strips a leading numbering prefix in either form:
	//   - "第一章 " / "第1条 " / "第(一)节 "
	//   - "1.1 " / "3) " / "2． "
	// Literal Unicode punctuation (． and 、) is used because Go's regexp
	// (RE2) does not support \uXXXX escapes.
	reLeadCNAndNumber = regexp.MustCompile(
		`^(第[一二三四五六七八九十百千0-9()]+[章节条款项段部分]?|[0-9]+([.．]\d+)*[.．\s、)]*)`,
	)
	reCollapseSpace = regexp.MustCompile(`\s+`)
)
